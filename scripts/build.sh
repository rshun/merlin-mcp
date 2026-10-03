#!/usr/bin/env bash
# 构建 linux/amd64 发布包：bash scripts/build.sh [版本]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always --dirty)}"
NAME="merlin-mcp_${VERSION}_linux_amd64"
OUT="dist/$NAME"

mkdir -p "$OUT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$OUT/merlin-mcp" ./cmd/merlin-mcp
cp deploy/config.example.yaml deploy/merlin-mcp.service deploy/install.sh "$OUT/"
tar -C dist -czf "dist/$NAME.tar.gz" "$NAME"
echo "dist/$NAME.tar.gz"
