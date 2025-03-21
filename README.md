# WebTermTCP

WebTermTCP - Web version of QtTermTCP

1) Clone this repo
2) Download prebuild binary from Releases (or build your own)
3) Run the program from within the cloned repo directory
4) In a browser go to http://127.0.0.1:5000 (or whatever IP address of the host)

Probably easiest to run this on the same host as BPQ32. Ensure you have the FBB port configured.

```
Usage of build/tcpproxy-linux-amd64:
  -debug
    	Enable debug logging for hex data and TCP write logs
  -listen-port int
    	Port on which the HTTP server listens (default 8000)
  -web-root string
    	Root directory for static files (default ".")
```
