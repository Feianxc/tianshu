#!/usr/bin/env bash
# Build the console, run the checks, and compile the binaries.
# Output: .build/ for this platform, .build/linux/ for Linux amd64.
set -euo pipefail
cd "$(dirname "$0")/.."
ext=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ext=".exe" ;; esac
(cd web && { [ -d node_modules ] || npm ci; } && npm run build)
cd server
go vet ./...
go test ./...
go build -o "../.build/wanglou$ext" ./cmd/wanglou
go build -o "../.build/wanglou-agent$ext" ./cmd/wanglou-agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ../.build/linux/wanglou ./cmd/wanglou
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ../.build/linux/wanglou-agent ./cmd/wanglou-agent
ls -la ../.build ../.build/linux
