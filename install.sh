#!/bin/sh
# SSH MCP installer for macOS and Linux.
#
# Install or update (the same command does both):
#   curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh
# Uninstall (add --purge to also delete your server list):
#   curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh -s -- --uninstall
#
# It downloads the right program for this computer from GitHub, checks its
# SHA-256 checksum, installs it to ~/.local/bin, and starts the guided setup
# on a first install. Updates keep your servers and AI app connections.
# Nothing is sent anywhere: no analytics, no telemetry.
#
# Options (environment variables):
#   SSH_MCP_VERSION=v1.2.0      install a specific release instead of the latest
#   SSH_MCP_INSTALL_DIR=/path   install somewhere other than ~/.local/bin
#   SSH_MCP_DOWNLOAD_URL=url    download from a mirror that has the release files
#   SSH_MCP_NO_SETUP=1          skip the guided setup

set -eu

REPO="anand34577/ssh-mcp"
NAME="ssh-mcp-server"
INSTALL_DIR="${SSH_MCP_INSTALL_DIR:-$HOME/.local/bin}"
TARGET="$INSTALL_DIR/$NAME"

say() { printf '%s\n' "$*"; }
fail() { printf 'Install stopped: %s\n' "$*" >&2; exit 1; }

# Runs the program with answers read from the terminal: when this script is
# piped from curl, its own stdin is the script.
interactive() {
  if [ -r /dev/tty ] && (exec </dev/tty) 2>/dev/null; then
    "$@" </dev/tty
  else
    return 2
  fi
}

case "${1:-}" in
  --uninstall)
    if [ -x "$TARGET" ]; then
      # Removes SSH MCP from every AI app, then deletes the program.
      if [ "${2:-}" = "--purge" ]; then
        "$TARGET" uninstall -yes -purge
      else
        "$TARGET" uninstall -yes
      fi
    fi
    rm -f "$TARGET"
    say "SSH MCP is uninstalled."
    exit 0
    ;;
  "") ;;
  *) fail "unknown option $1 (use --uninstall, optionally with --purge)" ;;
esac

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "this installer is for macOS and Linux. On Windows, use install.ps1." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported processor $(uname -m). Download a build from https://github.com/$REPO/releases" ;;
esac
asset="$NAME-$os-$arch"

if [ -n "${SSH_MCP_DOWNLOAD_URL:-}" ]; then
  base="${SSH_MCP_DOWNLOAD_URL%/}"
elif [ -n "${SSH_MCP_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$SSH_MCP_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi

if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is needed to download SSH MCP."
fi

previous=""
if [ -x "$TARGET" ]; then
  previous="$("$TARGET" version 2>/dev/null || true)"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading SSH MCP for $os/$arch..."
download "$base/$asset" "$tmp/$asset" || fail "could not download $base/$asset"
download "$base/$asset.sha256" "$tmp/$asset.sha256" || fail "could not download the checksum file"

expected="$(awk '{print $1}' "$tmp/$asset.sha256")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  fail "the download is damaged or was changed (checksum mismatch). Please try again."
fi
say "Checksum verified."

mkdir -p "$INSTALL_DIR"
chmod +x "$tmp/$asset"
# Moving (not copying) over the old file is safe even while an AI app runs it.
mv -f "$tmp/$asset" "$TARGET"
if [ "$os" = darwin ]; then
  # Allow macOS to run a program downloaded from the internet.
  xattr -d com.apple.quarantine "$TARGET" 2>/dev/null || true
fi
current="$("$TARGET" version)"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say ""
    say "Tip: $INSTALL_DIR is not on your PATH. To run ssh-mcp-server by name, add this"
    say "line to your shell profile (~/.zshrc or ~/.bashrc):"
    say "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

if [ -n "$previous" ]; then
  if [ "$previous" = "$current" ]; then
    say "You already have the latest version (${current#ssh-mcp-server })."
  else
    say "Updated from ${previous#ssh-mcp-server } to ${current#ssh-mcp-server }."
    say "Your servers and AI app connections were kept. Restart your AI apps to use the new version."
  fi
  exit 0
fi

say "Installed ${current#ssh-mcp-server } to $TARGET"
if [ "${SSH_MCP_NO_SETUP:-}" = "1" ] || ! interactive "$TARGET" setup; then
  say ""
  say "Run '$TARGET setup' whenever you're ready to add a server."
fi
