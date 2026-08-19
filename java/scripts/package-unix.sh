#!/usr/bin/env sh
set -eu

REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${JAVA_HOME:?Set JAVA_HOME to a JDK 11 installation}"
JAVA_HOME=$(CDPATH= cd -- "$JAVA_HOME" && pwd)
export JAVA_HOME
export PATH="$JAVA_HOME/bin:$PATH"

case "$(java -version 2>&1)" in
  *'version "11.'*) ;;
  *) echo "Packaging must use JDK 11" >&2; exit 1 ;;
esac

cd "$REPO"
mvn -q clean package

DIST="$REPO/dist"
RUNTIME="$DIST/runtime"
APP="$DIST/app"
rm -rf "$RUNTIME" "$APP"
mkdir -p "$APP"
jlink --add-modules java.base,java.logging,java.naming,java.management,jdk.crypto.ec,jdk.crypto.cryptoki,java.security.jgss \
  --strip-debug --no-man-pages --no-header-files --compress=2 --output "$RUNTIME"
cp "$REPO/target/ssh-mcp-server.jar" "$APP/ssh-mcp-server.jar"

mkdir -p "$DIST/bin"
cat > "$DIST/bin/ssh-mcp-server" <<'LAUNCHER'
#!/usr/bin/env sh
set -eu
APP_HOME=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec "$APP_HOME/runtime/bin/java" -jar "$APP_HOME/app/ssh-mcp-server.jar" "$@"
LAUNCHER
chmod +x "$DIST/bin/ssh-mcp-server"

if command -v jpackage >/dev/null 2>&1; then
  NATIVE="$DIST/native"
  rm -rf "$NATIVE"
  mkdir -p "$NATIVE"
  jpackage --type app-image --name ssh-mcp-server --app-version 1.0.0 \
    --input "$APP" --main-jar ssh-mcp-server.jar --main-class com.example.sshmcp.Main \
    --runtime-image "$RUNTIME" --dest "$NATIVE" --vendor "SSH MCP Server"
  echo "Native app image: $NATIVE/ssh-mcp-server"
else
  echo "Portable Unix distribution created. Install JDK 14+ to additionally create a native app image with jpackage."
fi
echo "Portable distribution: $DIST"
