#!/usr/bin/env sh
# Runs the Go tests and builds one binary into go/dist.
#   GOOS/GOARCH  target platform (default: this machine)
#   VERSION      version stamped into the binary (default: git describe, or "dev")
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
GO_BIN=${GO_BIN:-go}
GOOS_VALUE=${GOOS:-$("$GO_BIN" env GOOS)}
GOARCH_VALUE=${GOARCH:-$("$GO_BIN" env GOARCH)}
VERSION_VALUE=${VERSION:-$(git -C "$PROJECT_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}
OUTPUT_DIRECTORY="$PROJECT_ROOT/dist"
OUTPUT="$OUTPUT_DIRECTORY/ssh-mcp-server-$GOOS_VALUE-$GOARCH_VALUE"
if [ "$GOOS_VALUE" = "windows" ]; then
  OUTPUT="$OUTPUT.exe"
fi

mkdir -p "$OUTPUT_DIRECTORY"
cd "$PROJECT_ROOT"
"$GO_BIN" test ./...
CGO_ENABLED=0 GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" "$GO_BIN" build \
  -trimpath -ldflags "-s -w -X main.version=$VERSION_VALUE" -o "$OUTPUT" ./cmd/ssh-mcp-server

printf '%s\n' "Built $OUTPUT for $GOOS_VALUE/$GOARCH_VALUE ($VERSION_VALUE)"
