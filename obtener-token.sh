#!/usr/bin/env bash
# Compatibility helper for source builds. Release users run the native binary.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
bin=${AULAGLOBAL_MCP_BIN:-./aulaglobal-mcp}
if [[ ! -x "$bin" ]]; then
    printf '¿Quieres compilarlo con Docker? [s/N] '
    IFS= read -r reply
    case "$reply" in
        s|S|si|sí)
            docker run --rm -u "$(id -u):$(id -g)" -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
                -v "$PWD":/src -w /src golang:1.26.9 sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o aulaglobal-mcp ./cmd/aulaglobal-mcp'
            bin=./aulaglobal-mcp ;;
        *) exit 0 ;;
    esac
fi
exec "$bin" setup "$@"
