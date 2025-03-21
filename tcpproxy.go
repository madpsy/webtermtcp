// tcpproxy.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/zishang520/engine.io/v2/types"
	"github.com/zishang520/socket.io/v2/socket"
)

var (
	// Command-line arguments.
	listenPort = flag.Int("listen-port", 8000, "Port on which the HTTP server listens")
	webRoot    = flag.String("web-root", ".", "Root directory for static files")
	debug      = flag.Bool("debug", false, "Enable debug logging for hex data and TCP write logs")

	// Map for storing per-client TCP connections.
	tcpConns      = make(map[string]net.Conn)
	tcpConnsMutex sync.Mutex

	// Socket.IO client list.
	clients      []*socket.Socket
	clientsMutex sync.Mutex
)

func main() {
	flag.Parse()

	// Create an Engine.IO server and wrap it with a Socket.IO server.
	engineServer := types.CreateServer(nil)
	ioServer := socket.NewServer(engineServer, nil)

	// Handle new Socket.IO client connections.
	ioServer.On("connection", func(args ...any) {
		client := args[0].(*socket.Socket)
		log.Printf("Socket.IO client connected: %s", client.Id())

		clientsMutex.Lock()
		clients = append(clients, client)
		clientsMutex.Unlock()

		// When client sends "data", forward it to its associated TCP connection.
		client.On("data", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			var msg []byte
			switch v := datas[0].(type) {
			case []byte:
				msg = v
			case string:
				msg = []byte(v)
			default:
				log.Printf("Unexpected data type from client %s: %T", client.Id(), v)
				return
			}
			// Log hex data only if the debug flag is enabled.
			if *debug {
				log.Printf("Received %d bytes from Socket.IO client %s, forwarding to its TCP host: %X", len(msg), client.Id(), msg)
			}
			tcpConnsMutex.Lock()
			conn, exists := tcpConns[string(client.Id())]
			if exists && conn != nil {
				n, err := conn.Write(msg)
				if err != nil {
					log.Printf("Error writing to TCP connection for client %s: %v", client.Id(), err)
				} else if *debug {
					log.Printf("Wrote %d bytes to TCP host for client %s", n, client.Id())
				}
			} else {
				log.Printf("No TCP connection available for client %s", client.Id())
			}
			tcpConnsMutex.Unlock()
		})

		// Remove client on disconnect and close its associated TCP connection.
		client.On("disconnect", func(datas ...any) {
			log.Printf("Socket.IO client disconnected: %s", client.Id())
			clientsMutex.Lock()
			for i, c := range clients {
				if c == client {
					clients = append(clients[:i], clients[i+1:]...)
					break
				}
			}
			clientsMutex.Unlock()

			tcpConnsMutex.Lock()
			key := string(client.Id())
			if conn, exists := tcpConns[key]; exists && conn != nil {
				conn.Close()
				delete(tcpConns, key)
				log.Printf("TCP connection for client %s closed due to websocket disconnect", client.Id())
			}
			tcpConnsMutex.Unlock()
		})
	})

	// REST API endpoint to connect/disconnect the TCP connection for a specific client.
	http.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST is allowed", http.StatusMethodNotAllowed)
			return
		}
		// Expected JSON structure.
		var req struct {
			Action   string `json:"action"`    // "connect" or "disconnect"
			ClientID string `json:"client_id"` // Unique identifier for the WebSocket client
			Host     string `json:"host"`
			Port     int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		switch req.Action {
		case "connect":
			addr := fmt.Sprintf("%s:%d", req.Host, req.Port)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				log.Printf("Failed to connect to %s for client %s: %v", addr, req.ClientID, err)
				json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
				return
			}
			tcpConnsMutex.Lock()
			// Close any existing connection for this client before replacing.
			if oldConn, exists := tcpConns[req.ClientID]; exists && oldConn != nil {
				oldConn.Close()
			}
			tcpConns[req.ClientID] = conn
			tcpConnsMutex.Unlock()
			log.Printf("Connected to %s for client %s", addr, req.ClientID)
			// Start a goroutine to read from the TCP connection and forward data to the specific client.
			go streamTCPData(req.ClientID, conn)
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		case "disconnect":
			tcpConnsMutex.Lock()
			if conn, exists := tcpConns[req.ClientID]; exists && conn != nil {
				conn.Close()
				delete(tcpConns, req.ClientID)
				log.Printf("TCP connection for client %s disconnected", req.ClientID)
			}
			tcpConnsMutex.Unlock()
			notifyTCPDisconnect(req.ClientID, "Disconnected by user")
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
		}
	})

	// Serve static files from webRoot.
	fileServer := http.FileServer(http.Dir(*webRoot))
	http.Handle("/", fileServer)
	// Attach the Socket.IO endpoint.
	http.Handle("/socket.io/", engineServer)

	addr := fmt.Sprintf(":%d", *listenPort)
	log.Printf("Server listening on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for termination signal.
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-exit
	log.Println("Shutting down server...")
	os.Exit(0)
}

// streamTCPData reads from the TCP connection associated with a client and forwards any received data
// to that specific Socket.IO client using the "data" event.
func streamTCPData(clientID string, conn net.Conn) {
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from TCP connection for client %s: %v", clientID, err)
			}
			break
		}
		if n > 0 {
			data := buf[:n]
			// Log hex data only if the debug flag is enabled.
			if *debug {
				log.Printf("Received %d bytes from TCP host for client %s: %X", n, clientID, data)
			}
			clientsMutex.Lock()
			for _, client := range clients {
				if string(client.Id()) == clientID {
					go func(c *socket.Socket, d []byte) {
						if err := c.Emit("data", d); err != nil {
							log.Printf("Error sending data to client %s: %v", c.Id(), err)
						}
					}(client, data)
					break
				}
			}
			clientsMutex.Unlock()
		}
	}
	// Clean up the TCP connection mapping if this is still the active connection.
	tcpConnsMutex.Lock()
	if currentConn, exists := tcpConns[clientID]; exists && currentConn == conn {
		delete(tcpConns, clientID)
	}
	tcpConnsMutex.Unlock()
	log.Printf("TCP connection closed for client %s", clientID)
	notifyTCPDisconnect(clientID, "Remote TCP connection closed")
}

// notifyTCPDisconnect notifies a specific Socket.IO client that its TCP connection has been closed.
func notifyTCPDisconnect(clientID, message string) {
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	for _, client := range clients {
		if string(client.Id()) == clientID {
			go func(c *socket.Socket) {
				if err := c.Emit("tcp_disconnect", message); err != nil {
					log.Printf("Error notifying client %s: %v", c.Id(), err)
				}
			}(client)
			break
		}
	}
}
