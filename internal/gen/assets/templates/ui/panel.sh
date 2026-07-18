#!/usr/bin/env bash
# El PANEL del harness (`make ui`). Prefiere el daemon Go — trae multi-máquina,
# terminales en vivo, sonda de MCP, archivar, liveness honesta y el wizard de
# init. Orden: 1) `harness` instalado por brew (el camino canónico:
# brew install andresgarcia29/agm/harness), 2) el binario local bajado de un
# release, 3) el panel Python (server.py) — funciona, pero sin esas features.
# Solo lectura, solo 127.0.0.1.
set -euo pipefail
PORT="${1:-7717}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VER="0.46.0"
REPO="andresgarcia29/harness-daemon"
BIN="$DIR/harnessd"

# 1) el binario de brew: un solo gestor de versiones, cero descargas manuales
if command -v harness >/dev/null 2>&1; then
  opener=open; command -v xdg-open >/dev/null 2>&1 && opener=xdg-open
  ( sleep 1.2; "$opener" "http://127.0.0.1:$PORT" >/dev/null 2>&1 || true ) &
  exec harness run --port "$PORT" --workspace .
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"; case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
asset="harnessd-${os}-${arch}"

have=""; [ -x "$BIN" ] && have="$("$BIN" version 2>/dev/null || true)"

# Bajar/actualizar el binario si no tenemos la versión correcta.
if [ "$have" != "$VER" ] && command -v gh >/dev/null 2>&1; then
  echo "→ bajando $asset v$VER del release privado…"
  tmp="$(mktemp -d)"
  if gh release download "v$VER" -R "$REPO" -p "$asset" -D "$tmp" 2>/dev/null && [ -f "$tmp/$asset" ]; then
    mv "$tmp/$asset" "$BIN" && chmod +x "$BIN" && have="$VER"
    echo "  ✓ harnessd $VER instalado"
  fi
  rm -rf "$tmp"
fi

# Arrancar harnessd si lo tenemos (aunque sea una versión previa).
if [ -x "$BIN" ] && [ -n "$have" ]; then
  [ "$have" != "$VER" ] && echo "ℹ️  uso el harnessd que tienes (v$have); para el v$VER necesitas acceso al repo privado."
  opener=open; command -v xdg-open >/dev/null 2>&1 && opener=xdg-open
  ( sleep 1.2; "$opener" "http://127.0.0.1:$PORT" >/dev/null 2>&1 || true ) &
  exec "$BIN" run --port "$PORT" --workspace .
fi

# Sin binario ni forma de bajarlo → panel Python (sin las features nuevas).
echo "⚠️  no hay harnessd — caigo al panel Python (server.py): funciona, pero SIN"
echo "   multi-máquina, terminales ni sonda de MCP."
echo "   Para el panel completo, con acceso al repo privado:"
echo "     gh release download v$VER -R $REPO -p $asset -O $BIN && chmod +x $BIN"
exec python3 "$DIR/server.py" --port "$PORT" --workspace . --open
