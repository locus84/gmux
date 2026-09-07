#!/usr/bin/env bash
# Build and install only the web frontend assets, without stopping gmuxd.
#
# Configure gmuxd once to serve this directory, then future runs update the UI
# without replacing the daemon binary:
#
#   web_dir = "~/.local/state/gmux/web"
#
# Usage: ./scripts/install-web.sh [--dir <target-dir>]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${GMUX_WEB_INSTALL_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/gmux/web}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      TARGET="${2:?--dir requires a path}"
      shift 2
      ;;
    -h|--help)
      sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

case "$TARGET" in
  ~/*) TARGET="$HOME/${TARGET#~/}" ;;
esac
TARGET="$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$TARGET")"
PARENT="$(dirname "$TARGET")"
BASE="$(basename "$TARGET")"
NEXT="$PARENT/.$BASE.next.$$"
PREV="$PARENT/.$BASE.prev.$$"

cleanup() {
  rm -rf "$NEXT"
}
trap cleanup EXIT

echo "-> Building frontend..."
(cd "$ROOT/apps/gmux-web" && pnpm build)

echo "-> Installing web assets to $TARGET..."
mkdir -p "$PARENT"
rm -rf "$NEXT" "$PREV"
mkdir -p "$NEXT"
cp -R "$ROOT/apps/gmux-web/dist/." "$NEXT/"

if [[ -e "$TARGET" ]]; then
  mv "$TARGET" "$PREV"
fi
if ! mv "$NEXT" "$TARGET"; then
  if [[ -e "$PREV" && ! -e "$TARGET" ]]; then
    mv "$PREV" "$TARGET"
  fi
  exit 1
fi
rm -rf "$PREV"
trap - EXIT

echo "✓ Web assets installed"
echo "Configure gmuxd with web_dir = \"$TARGET\" in ${XDG_CONFIG_HOME:-$HOME/.config}/gmux/host.toml"
