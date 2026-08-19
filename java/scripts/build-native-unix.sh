#!/usr/bin/env sh
set -eu

REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${GRAALVM_HOME:?Set GRAALVM_HOME to a GraalVM installation with native-image}"
GRAALVM_HOME=$(CDPATH= cd -- "$GRAALVM_HOME" && pwd)
export JAVA_HOME="$GRAALVM_HOME"
export PATH="$GRAALVM_HOME/bin:$PATH"

command -v native-image >/dev/null 2>&1 || {
  echo "native-image is not available in GRAALVM_HOME" >&2
  exit 1
}

cd "$REPO"
mvn -q clean package
mkdir -p "$REPO/dist/native-image"
native-image --no-fallback -H:+ReportExceptionStackTraces \
  -H:Name="$REPO/dist/native-image/ssh-mcp-server" \
  -jar "$REPO/target/ssh-mcp-server.jar"
echo "Self-contained native executable: $REPO/dist/native-image/ssh-mcp-server"
