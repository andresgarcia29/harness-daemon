#!/usr/bin/env bash
# repo-brief.sh — brief determinista de un repo, $0 tokens.
#
# El arranque frío de un implementer gasta sus primeros minutos (y miles de
# tokens) re-descubriendo lo mismo: estructura, comandos de test, convenciones.
# Este script lo destila UNA vez por HEAD y lo cachea: el orquestador pasa el
# brief en el prompt y el implementer arranca ya orientado, sin explorar.
#
# Uso: repo-brief.sh <repo> [--force]
# Salida: .cache/briefs/<repo>.md (regenera solo si el HEAD del repo cambió)
# Portabilidad: bash 3.2 (macOS), BSD tools. Fail-open: un brief que no se
# pudo generar no bloquea nada — el implementer simplemente explora como antes.
set -u

REPO="${1:?uso: repo-brief.sh <repo> [--force]}"
FORCE="${2:-}"
WS="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$WS/repos/$REPO"
OUT_DIR="$WS/.cache/briefs"
OUT="$OUT_DIR/$REPO.md"

[ -d "$SRC/.git" ] || { echo "⚠️  no existe repos/$REPO — sin brief" >&2; exit 0; }
mkdir -p "$OUT_DIR"

head_sha="$(git -C "$SRC" rev-parse HEAD 2>/dev/null || echo unknown)"
if [ "$FORCE" != "--force" ] && [ -f "$OUT" ] && head -1 "$OUT" | grep -qF "$head_sha"; then
  echo "$OUT"   # cache vigente
  exit 0
fi

# Sección acotada: nunca más de $2 líneas de la fuente $1 (economía de tokens).
capped() { [ -f "$1" ] && sed -n "1,${2}p" "$1"; }

{
  echo "<!-- brief @ $head_sha — generado por repo-brief.sh; NO editar -->"
  echo "# $REPO — brief"
  echo

  echo "## Stack y comandos"
  [ -f "$SRC/go.mod" ]        && echo "- Go (\`go build ./...\` · \`go test ./...\` · module: $(head -1 "$SRC/go.mod" | cut -d' ' -f2))"
  [ -f "$SRC/package.json" ]  && echo "- Node$( [ -f "$SRC/tsconfig.json" ] && printf '/TypeScript (`npx tsc --noEmit`)' ) — scripts: $(command -v jq >/dev/null && jq -r '.scripts | keys | join(", ")' "$SRC/package.json" 2>/dev/null || echo "ver package.json")"
  [ -f "$SRC/pyproject.toml" ] && echo "- Python (\`ruff check .\` · \`pytest -q\`)"
  [ -f "$SRC/pubspec.yaml" ]  && echo "- Flutter (\`flutter analyze\` · \`flutter test\`)"
  [ -f "$SRC/buf.yaml" ]      && echo "- ⚠️ CONTRATOS proto (buf): cualquier cambio aquí es carril standard/full, expand/contract obligatorio"
  if [ -f "$SRC/Makefile" ]; then
    targets="$(grep -E '^[a-zA-Z0-9_.-]+:([^=]|$)' "$SRC/Makefile" | cut -d: -f1 | sort -u | head -12 | tr '\n' ' ')"
    [ -n "$targets" ] && echo "- Makefile: $targets"
  fi
  echo

  echo "## Estructura (2 niveles, solo directorios)"
  (cd "$SRC" && find . -maxdepth 2 -type d \
      ! -path './.git*' ! -path './node_modules*' ! -path './vendor*' \
      ! -path './dist*' ! -path './build*' ! -path './.cache*' \
      | sort | head -40 | sed 's|^\./||; s|^|  |')
  echo

  if [ -f "$SRC/CLAUDE.md" ]; then
    echo "## CLAUDE.md del repo (primeras 40 líneas — la fuente manda)"
    capped "$SRC/CLAUDE.md" 40
    echo
  elif [ -f "$SRC/README.md" ]; then
    echo "## README (primeras 20 líneas)"
    capped "$SRC/README.md" 20
    echo
  fi

  echo "## Tests: dónde viven"
  (cd "$SRC" && find . -maxdepth 3 -type d \( -name 'test' -o -name 'tests' -o -name '__tests__' -o -name 'spec' \) \
      ! -path './.git*' ! -path './node_modules*' ! -path './vendor*' \
      | sort | head -10 | sed 's|^\./||; s|^|  |')
  (cd "$SRC" && find . -maxdepth 2 -name '*_test.go' -o -maxdepth 2 -name '*.test.ts' -o -maxdepth 2 -name 'test_*.py' 2>/dev/null | head -5 | sed 's|^\./||; s|^|  |')
} > "$OUT" 2>/dev/null || { rm -f "$OUT"; echo "⚠️  brief de $REPO falló — fail-open" >&2; exit 0; }

echo "$OUT"
