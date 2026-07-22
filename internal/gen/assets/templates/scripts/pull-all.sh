#!/usr/bin/env bash
# pull-all.sh: TODOS los clones canónicos al último main, EN PARALELO.
#
# "Empezar con lo más nuevo" es preparación determinista: cuesta $0 tokens y
# evita la peor clase de retrabajo (implementar sobre una base vieja). Reglas:
#   · paralelo total: el cuello es la red, no el CPU
#   · un clon SUCIO se salta con aviso (el canónico debe estar limpio; los
#     cambios viven en worktrees, y un pull --rebase sobre mugre es peligro)
#   · una rama distinta de main se reporta (no se hace checkout automático)
#   · al final, si algún HEAD se movió, el grafo se refresca en background
# Portabilidad: bash 3.2 (macOS), sin GNU-ismos. Exit 1 si algún pull FALLÓ
# (red/conflicto); los saltados por mugre no son fallo, son aviso.
set -u

WS="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$(mktemp -d "${TMPDIR:-/tmp}/pull-all.XXXXXX")"
trap 'rm -rf "$OUT"' EXIT

pull_one() {  # pull_one <dir> <slot>
  local d="$1" slot="$2" name branch note="" before after out
  name="$(basename "$d")"
  [ "$d" = "$WS" ] && name="(workspace)"
  if [ -n "$(git -C "$d" status --porcelain 2>/dev/null)" ]; then
    echo "○ $name: cambios locales en el clon canónico; lo salto (el trabajo va en worktrees)" > "$OUT/$slot.txt"
    echo 2 > "$OUT/$slot.rc"
    return
  fi
  branch="$(git -C "$d" symbolic-ref --short HEAD 2>/dev/null || echo desconocida)"
  case "$branch" in main|master) ;; *) note=" [rama: $branch]" ;; esac
  before="$(git -C "$d" rev-parse --short HEAD 2>/dev/null)"
  if out="$(git -C "$d" pull --rebase 2>&1)"; then
    after="$(git -C "$d" rev-parse --short HEAD 2>/dev/null)"
    if [ "$before" = "$after" ]; then
      echo "✓ $name: ya al día ($after)$note" > "$OUT/$slot.txt"
    else
      echo "✓ $name: $before → $after$note" > "$OUT/$slot.txt"
      echo 1 > "$OUT/$slot.moved"
    fi
    echo 0 > "$OUT/$slot.rc"
  else
    echo "✗ $name: $(printf '%s' "$out" | tail -1)$note" > "$OUT/$slot.txt"
    echo 1 > "$OUT/$slot.rc"
  fi
}

slot=0
PIDS=""
for d in "$WS"/repos/*/; do
  [ -d "$d/.git" ] || continue
  slot=$((slot+1))
  pull_one "${d%/}" "$slot" &
  PIDS="$PIDS $!"
done
if [ -d "$WS/.git" ]; then
  slot=$((slot+1))
  pull_one "$WS" "$slot" &
  PIDS="$PIDS $!"
fi
[ "$slot" -gt 0 ] || { echo "sin repos git (ni en repos/ ni el workspace)"; exit 0; }

for p in $PIDS; do wait "$p" 2>/dev/null; done

for f in "$OUT"/*.txt; do cat "$f"; done | sort
fails=0
for f in "$OUT"/*.rc; do [ "$(cat "$f")" = "1" ] && fails=$((fails+1)); done

# HEADs nuevos = grafo viejo: refresh en background, fail-open, sin bloquear
if ls "$OUT"/*.moved >/dev/null 2>&1 && [ -x "$WS/scripts/graph-refresh.sh" ]; then
  (bash "$WS/scripts/graph-refresh.sh" >/dev/null 2>&1 &)
  echo "── grafo: refresh disparado en background (HEADs nuevos)"
fi

if [ "$fails" -gt 0 ]; then
  echo "── $fails pull(s) FALLARON (red o conflicto de rebase); detalle arriba"
  exit 1
fi
echo "── todo al día: $slot repos en paralelo"
