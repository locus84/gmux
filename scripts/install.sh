#!/usr/bin/env bash
# Build and install gmux + gmuxd to ~/.local/bin.
#
# Stops the running gmuxd, replaces the binaries, and restarts it.
# Active sessions survive (the new daemon rediscovers them on startup);
# the running runner keeps its old binary until that session restarts.
#
# Usage: ./scripts/install.sh [--skip-frontend]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_DIR="${GMUX_INSTALL_DIR:-$HOME/.local/bin}"
SKIP_FRONTEND=false
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) SKIP_FRONTEND=true ;;
  esac
done

# ── Build ──

"$ROOT/scripts/build.sh" "$@"

# ── Stop gmuxd ──

echo "→ Stopping gmuxd..."
# Kill any running gmuxd (installed or dev). Multiple instances would
# fight over the socket, so be thorough.
for pid in $(pgrep -x gmuxd 2>/dev/null); do
  kill "$pid" 2>/dev/null || true
done
sleep 1
# Verify none survived.
if pgrep -x gmuxd >/dev/null 2>&1; then
  kill -9 $(pgrep -x gmuxd) 2>/dev/null || true
  sleep 1
fi

# ── Install ──
#
# Remove before copy: the old binary may be held open by running gmux
# processes. Removing the inode lets us write a new file at the same path
# while the old processes continue using the deleted inode.

echo "→ Installing to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"

for bin in gmux gmuxd; do
  rm -f "$INSTALL_DIR/$bin"
  cp "$ROOT/bin/$bin" "$INSTALL_DIR/$bin"
done

echo ""
ls -lh "$INSTALL_DIR/gmux" "$INSTALL_DIR/gmuxd"

# An external web_dir takes precedence over the assets embedded above. Keep
# that configured directory in lockstep with the binaries, otherwise a normal
# install appears to succeed while gmuxd continues serving an older UI.
if [ "$SKIP_FRONTEND" = false ]; then
  WEB_DIR="${GMUXD_WEB_DIR:-}"
  HOST_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/gmux/host.toml"
  if [[ -z "$WEB_DIR" && -f "$HOST_CONFIG" ]]; then
    # host.toml documents web_dir as a top-level TOML basic string. Keep the
    # parser narrow and dependency-free so this also works with macOS Python 3.9.
    WEB_DIR="$(sed -nE 's/^[[:space:]]*web_dir[[:space:]]*=[[:space:]]*"([^"]*)"[[:space:]]*(#.*)?$/\1/p' "$HOST_CONFIG" | head -n 1)"
  fi
  if [[ -n "$WEB_DIR" ]]; then
    echo "→ Updating configured web assets..."
    "$ROOT/scripts/install-web.sh" --skip-build --dir "$WEB_DIR"
  fi
fi

# ── Restart gmuxd ──

echo "→ Restarting gmuxd..."
if "$INSTALL_DIR/gmuxd" start; then
  echo "✓ gmuxd is running"
else
  echo "⚠ gmuxd failed to start."
  echo "  Check logs: ~/.local/state/gmux/gmuxd.log"
  exit 1
fi
