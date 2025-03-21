#!/bin/bash
set -e

# Create output directory.
mkdir -p build

# Build function for a given platform.
build_binary() {
  local os="$1"
  local arch="$2"
  local out="$3"
  echo "Building for ${os} (${arch})..."
  env GOOS="$os" GOARCH="$arch" go build -o "build/${out}" tcpproxy.go
}

# Build for each target platform.
build_binary linux   amd64   "tcpproxy-linux-amd64"
build_binary darwin  amd64   "tcpproxy-darwin-amd64"
build_binary windows amd64   "tcpproxy-windows-amd64.exe"
build_binary linux   arm     "tcpproxy-linux-arm"
build_binary linux   arm64   "tcpproxy-linux-arm64"

echo "All builds succeeded. Binaries are located in the 'build' directory."
