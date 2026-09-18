#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
go test ./internal/core
go vet ./internal/core
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags='-H=windowsgui -s -w' -o TypeNext.exe ./cmd/typenext
printf '%s\n' 'Built TypeNext.exe. Windows UI behavior was not exercised by this build.'
