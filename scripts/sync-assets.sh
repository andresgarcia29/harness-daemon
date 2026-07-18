#!/usr/bin/env bash
# sync-assets.sh — sincroniza los assets del instalador (harness-installer)
# hacia este repo para embeberlos en el binario (go:embed):
#
#   templates/        → internal/gen/assets/templates/   (sin ui/web ni ui/dist)
#   scripts/{discover,doctor}.sh → internal/gen/assets/scripts/
#   catalog/capabilities.yaml    → internal/gen/assets/catalog/capabilities.json
#   templates/ui/dist → internal/webui/dist              (el panel compilado)
#
# y escribe internal/gen/assets/manifest.json con el commit del instalador y
# el sha256 de cada archivo. Este script ES la cadena de suministro interna:
# sin él en CI (--check), un release embebería templates viejos en silencio.
#
# Uso:
#   scripts/sync-assets.sh [ruta-al-installer]          # sincroniza
#   scripts/sync-assets.sh --check [ruta-al-installer]  # verifica drift (CI)
set -euo pipefail

CHECK=0
if [ "${1:-}" = "--check" ]; then CHECK=1; shift; fi
DAEMON="$(cd "$(dirname "$0")/.." && pwd)"
INSTALLER="${1:-${INSTALLER_DIR:-$DAEMON/../harness-installer}}"
INSTALLER="$(cd "$INSTALLER" && pwd)" || { echo "❌ no encuentro el installer en $INSTALLER"; exit 1; }
[ -d "$INSTALLER/templates" ] || { echo "❌ $INSTALLER no parece harness-installer (sin templates/)"; exit 1; }

yaml2json() { # capabilities.yaml → json determinista (claves ordenadas)
  if python3 -c 'import yaml' 2>/dev/null; then
    python3 - "$1" <<'PY'
import json, sys, yaml
with open(sys.argv[1]) as f:
    data = yaml.safe_load(f)
print(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True))
PY
  elif command -v yq >/dev/null 2>&1; then
    yq -o=json '.' "$1"
  else
    echo "❌ necesito python3+pyyaml o yq para convertir el catálogo" >&2
    exit 1
  fi
}

build_assets() { # $1 = destino (un dir "assets" recién creado)
  local out="$1"
  mkdir -p "$out/templates" "$out/scripts" "$out/catalog"
  # templates sin el fuente del frontend ni el dist (el dist ya viaja en webui)
  (cd "$INSTALLER/templates" && find . -type f \
      ! -path "./ui/web/*" ! -path "./ui/dist/*" \
      ! -name "*.pyc" ! -path "*__pycache__*" ! -name ".DS_Store" \
      -exec sh -c 'mkdir -p "$0/$(dirname "$1")" && cp "$1" "$0/$1"' "$out/templates" {} \;)
  cp "$INSTALLER/scripts/discover.sh" "$INSTALLER/scripts/doctor.sh" "$out/scripts/"
  yaml2json "$INSTALLER/catalog/capabilities.yaml" > "$out/catalog/capabilities.json"
  # prompts del instalador para los pasos LLM (si existen)
  if [ -d "$INSTALLER/prompts" ]; then
    mkdir -p "$out/prompts" && cp "$INSTALLER/prompts/"*.md "$out/prompts/" 2>/dev/null || true
  fi
  # manifest: commit del instalador + sha256 por archivo (determinista, sin fechas)
  local commit
  commit="$(git -C "$INSTALLER" rev-parse HEAD 2>/dev/null || echo unknown)"
  (cd "$out" && {
    echo '{'
    echo "  \"installer_commit\": \"$commit\","
    echo '  "files": {'
    find . -type f ! -name manifest.json | LC_ALL=C sort | while read -r f; do
      printf '    "%s": "%s",\n' "${f#./}" "$(shasum -a 256 "$f" | cut -d' ' -f1)"
    done | sed '$ s/,$//'
    echo '  }'
    echo '}'
  } > manifest.json)
}

ASSETS="$DAEMON/internal/gen/assets"
WEBDIST="$DAEMON/internal/webui/dist"

if [ "$CHECK" = 1 ]; then
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  build_assets "$tmp/assets"
  drift=0
  if ! diff -r --exclude=manifest.json "$tmp/assets" "$ASSETS" >/dev/null 2>&1; then
    echo "❌ drift en internal/gen/assets — corre scripts/sync-assets.sh"
    diff -rq --exclude=manifest.json "$tmp/assets" "$ASSETS" 2>&1 | head -20 || true
    drift=1
  fi
  if ! diff -r "$INSTALLER/templates/ui/dist" "$WEBDIST" >/dev/null 2>&1; then
    echo "❌ drift en internal/webui/dist — corre scripts/sync-assets.sh"
    drift=1
  fi
  [ "$drift" = 0 ] && echo "✅ assets en sync (installer $(git -C "$INSTALLER" rev-parse --short HEAD 2>/dev/null || echo '?'))"
  exit "$drift"
fi

rm -rf "$ASSETS"
build_assets "$ASSETS"
rm -rf "$WEBDIST"
mkdir -p "$WEBDIST"
cp -r "$INSTALLER/templates/ui/dist/." "$WEBDIST/"
n="$(find "$ASSETS" -type f | wc -l | tr -d ' ')"
echo "✅ sync: $n archivos en internal/gen/assets + panel en internal/webui/dist"
echo "   installer @ $(git -C "$INSTALLER" rev-parse --short HEAD 2>/dev/null || echo '?')"
