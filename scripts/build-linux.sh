#!/usr/bin/env bash
set -euo pipefail

mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w" \
  -o dist/pg-migrate-linux-amd64 \
  ./cmd/pg-migrate

echo "Build selesai: dist/pg-migrate-linux-amd64"
