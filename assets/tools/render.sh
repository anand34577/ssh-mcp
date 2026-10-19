#!/usr/bin/env bash
# Renders the PNG versions of the SVG artwork with headless Chrome/Chromium.
# Usage: assets/tools/render.sh   (set CHROME=/path/to/chrome to override)
set -euo pipefail
cd "$(dirname "$0")/.."

chrome="${CHROME:-}"
if [ -z "$chrome" ]; then
  for candidate in google-chrome chromium chromium-browser \
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
      "/c/Program Files/Google/Chrome/Application/chrome.exe" \
      "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
    if command -v "$candidate" >/dev/null 2>&1 || [ -x "$candidate" ]; then chrome="$candidate"; break; fi
  done
fi
[ -n "$chrome" ] || { echo "Chrome or Chromium is needed; set CHROME=/path/to/chrome" >&2; exit 1; }

url() { # file URL for a path, on Windows (Git Bash) and Unix
  if command -v cygpath >/dev/null 2>&1; then echo "file:///$(cygpath -m "$(pwd)/$1")"; else echo "file://$(pwd)/$1"; fi
}
out() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$(pwd)/$1"; else echo "$(pwd)/$1"; fi; }

# render <svg> <png> <css width> <css height> [scale]
render() {
  "$chrome" --headless=new --disable-gpu --hide-scrollbars --default-background-color=00000000 \
    --force-device-scale-factor="${5:-1}" --window-size="$3,$4" --screenshot="$(out "$2")" "$(url "$1")" >/dev/null 2>&1
  echo "rendered $2"
}

python3 tools/setup-demo.py setup-demo.svg >/dev/null 2>&1 || python tools/setup-demo.py setup-demo.svg >/dev/null
render social-preview.svg social-preview.png 1280 640
render social-square.svg social-square.png 1080 1080
render banner.svg banner.png 1600 520
render logo.svg icon-512.png 512 512
render logo.svg icon-256.png 512 512 0.5
