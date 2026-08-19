#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
GO_BIN=${GO_BIN:-go}
GOOS_VALUE=${GOOS:-linux}
GOARCH_VALUE=${GOARCH:-amd64}
OUTPUT_DIRECTORY="$PROJECT_ROOT/dist"

mkdir -p "$OUTPUT_DIRECTORY"
cd "$PROJECT_ROOT"
"$GO_BIN" test ./...
CGO_ENABLED=0 GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" "$GO_BIN" build \
  -trimpath -ldflags '-s -w' -o "$OUTPUT_DIRECTORY/ssh-mcp-server" ./cmd/ssh-mcp-server

printf '%s\n' "Built $OUTPUT_DIRECTORY/ssh-mcp-server for $GOOS_VALUE/$GOARCH_VALUE"
