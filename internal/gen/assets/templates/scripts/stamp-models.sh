#!/usr/bin/env bash
# stamp-models.sh: materializa la política de models.yaml en los agentes.
# Determinista, $0 tokens, cero dependencias (awk de BSD basta).
#
# models.yaml habla en ALIASES (fast|smart|deep) y este script los traduce
# al ID real del proveedor activo y los estampa en el frontmatter `model:`
# de .claude/agents/*.md. Cambiar de modelo o de proveedor = editar UNA
# línea de models.yaml + `make models`. Nadie edita agentes a mano.
#
# Uso:
#   stamp-models.sh                       estampa .claude/agents/*.md
#   stamp-models.sh resolve <alias|rol>   imprime el ID real (para /auto
#                                         --model, cron-runner, headless)
#   stamp-models.sh check                 verifica sin tocar (doctor);
#                                         exit 1 si hay drift
#
# Portabilidad: bash 3.2 (macOS), sin sed -i. Formato de models.yaml:
# estricto de 2 niveles (secciones a columna 0, claves con 2 espacios).
set -euo pipefail

WS="$(cd "$(dirname "$0")/.." && pwd)"
MODELS="$WS/models.yaml"
AGENTS_DIR="$WS/.claude/agents"
[ -f "$MODELS" ] || { echo "❌ no existe $MODELS"; exit 2; }

yget() {  # yget <sección> <clave>: valor de una clave indentada bajo la sección
  awk -v s="$1:" -v k="$2:" '
    /^[^ #]/ { insec = ($1 == s) }
    insec && $1 == k && /^  / { sub(/^[ ]*[^ ]*[ ]*/, ""); sub(/[ ]*#.*$/, ""); print; exit }
  ' "$MODELS"
}

ytop() {  # ytop <clave>: escalar de nivel superior
  awk -v k="$1:" '
    /^[^ #]/ && $1 == k { sub(/^[^:]*:[ ]*/, ""); sub(/[ ]*#.*$/, ""); print; exit }
  ' "$MODELS"
}

PROVIDER="$(ytop provider)"
[ -n "$PROVIDER" ] || { echo "❌ models.yaml sin 'provider:'"; exit 2; }

resolve_alias() {  # alias → id del proveedor activo
  local id; id="$(yget "models.$PROVIDER" "$1")"
  [ -n "$id" ] || { echo "❌ alias '$1' no existe en la sección models.$PROVIDER de models.yaml" >&2; return 2; }
  printf '%s\n' "$id"
}

resolve() {  # <alias|rol> → id (un rol se traduce primero a su alias)
  local via_role; via_role="$(yget roles "$1")"
  resolve_alias "${via_role:-$1}"
}

role_for() {  # <basename de agente> → rol de models.yaml
  case "$1" in
    architect|implementer|reviewer|qa) printf '%s' "$1" ;;
    *) printf 'abogados' ;;   # svc-*, infra, frontends: todos litigan
  esac
}

expected_model() {  # <basename> → id esperado (override > rol)
  local ov; ov="$(yget overrides "$1")"
  if [ -n "$ov" ]; then resolve_alias "$ov"; else resolve "$(role_for "$1")"; fi
}

cmd="${1:-stamp}"
case "$cmd" in
  resolve)
    resolve "${2:?uso: stamp-models.sh resolve <alias|rol>}"
    ;;
  stamp|check)
    [ -d "$AGENTS_DIR" ] || { echo "❌ no existe $AGENTS_DIR"; exit 2; }
    drift=0 changed=0
    for f in "$AGENTS_DIR"/*.md; do
      [ -f "$f" ] || continue
      base="$(basename "$f" .md)"
      want="$(expected_model "$base")" || exit 2
      have="$(awk '/^model:/ { print $2; exit }' "$f")"
      [ "$have" = "$want" ] && continue
      if [ "$cmd" = "check" ]; then
        echo "✗ $base: model '$have' ≠ política '$want'"
        drift=1
      else
        awk -v m="$want" '{ if (!done && $0 ~ /^model:/) { $0 = "model: " m; done = 1 } print }' \
          "$f" > "$f.tmp" && mv "$f.tmp" "$f"
        echo "✓ $base → $want"
        changed=1
      fi
    done
    if [ "$cmd" = "check" ] && [ "$drift" -ne 0 ]; then
      echo "   ↳ remediación: make models (re-estampa desde models.yaml)"
      exit 1
    fi
    [ "$cmd" = "stamp" ] && [ "$changed" -eq 0 ] && echo "✓ agentes ya alineados con models.yaml (provider: $PROVIDER)"
    exit 0
    ;;
  *)
    echo "uso: stamp-models.sh [stamp|resolve <alias|rol>|check]"; exit 2
    ;;
esac
