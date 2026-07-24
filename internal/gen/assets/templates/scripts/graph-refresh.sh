#!/usr/bin/env bash
# graph-refresh.sh: mantiene VIVO el grafo de graphify. $0 tokens (tree-sitter).
#
# El hueco que cierra: los prompts dicen "usa graphify query, no grep masivo",
# pero una query contra un grafo inexistente o viejo falla o miente, y el
# agente cae a grep, pagando los tokens que el grafo existía para ahorrar.
# Este script es el ciclo de vida completo: construye la primera vez,
# refresca incremental después, y NO hace nada si ningún HEAD cambió.
#
# POR REPO Y LUEGO MERGE, no una pasada sobre repos/. Apuntar graphify al
# directorio padre de un montón de repos git independientes puede terminar en
# "No code files found - nothing to rebuild": un grafo de 0 nodos que ANTES
# reportábamos como sano (issue #25). Por repo funciona siempre, y
# `graphify merge-graphs` existe justo para unirlos en el grafo cross-repo.
#
# Y NO CONFÍA EN EL EXIT CODE: graphify sale 0 aunque no haya indexado nada,
# así que aquí se cuentan NODOS. Cero nodos es un fallo ruidoso, no un ✓, y
# no escribe stamp: el próximo refresh lo reintenta.
#
# Lo llaman: el bootstrap (build inicial en el onboarding), el prefetch de
# /auto y /rfc (background), harness-janitor (nightly) y tú (make graph).
# Fail-open: sin graphify instalado (capacidad no elegida) sale 0 en silencio;
# jamás bloquea un pipeline.
set -u

WS="$(cd "$(dirname "$0")/.." && pwd)"
command -v graphify >/dev/null 2>&1 || exit 0
[ -d "$WS/repos" ] || exit 0

GRAPH="$WS/graphify-out/graph.json"
STAMP="$WS/.cache/graph.stamp"
HEADS="$WS/.cache/graph-heads"      # un archivo por repo: su HEAD indexado
LOG="$WS/.cache/graph.log"          # la salida de graphify, NUNCA /dev/null
mkdir -p "$WS/.cache" "$HEADS" "$WS/graphify-out"

nodes_in() {  # nodos de un graph.json (0 si no existe o no parsea)
  local n; n="$(jq '.nodes | length' "$1" 2>/dev/null)"
  case "$n" in ''|*[!0-9]*) echo 0 ;; *) echo "$n" ;; esac
}

# huella global: la suma de HEADs de todos los repos (cksum es POSIX)
heads_sum="$(for d in "$WS"/repos/*/; do
  [ -d "$d/.git" ] && git -C "$d" rev-parse HEAD 2>/dev/null
done | sort | cksum | cut -d' ' -f1)"

# fresco = mismo estado Y grafo con contenido (un grafo vacío nunca es fresco)
if [ -f "$STAMP" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "$heads_sum" ] \
   && [ "$(nodes_in "$GRAPH")" -gt 0 ]; then
  exit 0
fi

cd "$WS"
: > "$LOG"
parts=""; built=0; empty=""; total_repos=0

for d in repos/*/; do
  [ -d "$d/.git" ] || continue
  total_repos=$((total_repos+1))
  name="$(basename "$d")"
  head="$(git -C "$d" rev-parse HEAD 2>/dev/null || echo none)"
  g="$d/graphify-out/graph.json"
  # incremental de verdad: solo re-extrae el repo cuyo HEAD cambió
  if [ "$(cat "$HEADS/$name" 2>/dev/null || echo x)" != "$head" ] || [ "$(nodes_in "$g")" -eq 0 ]; then
    echo "── $name" >> "$LOG"
    # `graphify update <path>` extrae SOLO código (AST, sin LLM ni API key).
    # El comando viejo `graphify <path>` intentaba extracción SEMÁNTICA de
    # docs/imágenes, que exige API key y fallaba en toda instancia sin ella.
    graphify update "$d" >> "$LOG" 2>&1 || true
  fi
  n="$(nodes_in "$g")"
  if [ "$n" -gt 0 ]; then
    printf '%s' "$head" > "$HEADS/$name"
    parts="$parts $g"; built=$((built+1))
  else
    empty="$empty $name"
  fi
done

if [ "$built" -eq 0 ]; then
  echo "⚠️  graphify no indexó NADA en repos/ ($total_repos repos): el grafo quedaría vacío" >&2
  echo "   ↳ últimas líneas de $LOG:" >&2; tail -5 "$LOG" >&2
  echo "   ↳ sin grafo, 'graphify query' no responde: los agentes caerán a grep masivo" >&2
  exit 0   # fail-open: ruidoso, pero jamás tumba a quien lo llamó
fi

# un solo repo no necesita merge; varios sí (el grafo es cross-repo)
# shellcheck disable=SC2086
if [ "$built" -eq 1 ]; then
  cp $parts "$GRAPH" 2>/dev/null || true
else
  graphify merge-graphs $parts --out "$GRAPH" >> "$LOG" 2>&1 || true
fi

nodes="$(nodes_in "$GRAPH")"
if [ "$nodes" -eq 0 ]; then
  echo "⚠️  el merge dejó un grafo VACÍO pese a $built repos indexados" >&2
  echo "   ↳ revisa $LOG; no marco el grafo como fresco (se reintenta al próximo refresh)" >&2
  exit 0
fi

printf '%s' "$heads_sum" > "$STAMP"
echo "✓ grafo al día: $nodes nodos de $built/$total_repos repos ($GRAPH)"
[ -n "$empty" ] && echo "  (sin código indexable:$empty)"
exit 0
