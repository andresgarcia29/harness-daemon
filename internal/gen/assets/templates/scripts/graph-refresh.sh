#!/usr/bin/env bash
# graph-refresh.sh: mantiene VIVO el grafo de graphify. $0 tokens (tree-sitter).
#
# El hueco que cierra: los prompts dicen "usa graphify query, no grep masivo",
# pero una query contra un grafo inexistente o viejo falla o miente, y el
# agente cae a grep, pagando los tokens que el grafo existía para ahorrar.
# Este script es el ciclo de vida completo: construye la primera vez,
# refresca incremental (--update) después, y NO hace nada si ningún HEAD
# cambió (stamp por suma de HEADs).
#
# Lo llaman: el prefetch de /auto y /rfc (background), harness-janitor
# (nightly) y tú (make graph). Fail-open: sin graphify instalado
# (capacidad no elegida) sale 0 en silencio; jamás bloquea un pipeline.
set -u

WS="$(cd "$(dirname "$0")/.." && pwd)"
command -v graphify >/dev/null 2>&1 || exit 0
[ -d "$WS/repos" ] || exit 0

STAMP="$WS/.cache/graph.stamp"
mkdir -p "$WS/.cache"

graph_json() {  # el output de graphify queda en graphify-out/ relativo al cwd
  [ -f "$WS/graphify-out/graph.json" ] && { echo "$WS/graphify-out/graph.json"; return 0; }
  [ -f "$WS/repos/graphify-out/graph.json" ] && { echo "$WS/repos/graphify-out/graph.json"; return 0; }
  return 1
}

# huella de estado: la suma de HEADs de todos los repos (cksum es POSIX)
heads="$(for d in "$WS"/repos/*/; do
  [ -d "$d/.git" ] && git -C "$d" rev-parse HEAD 2>/dev/null
done | sort | cksum | cut -d' ' -f1)"

if [ -f "$STAMP" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "$heads" ] && graph_json >/dev/null; then
  exit 0   # fresco: ningún HEAD cambió desde el último build
fi

cd "$WS"
# `graphify update <path>` extrae SOLO código (AST, sin LLM ni API key) y
# sirve para el build inicial Y el refresh: es idempotente. El comando viejo
# `graphify <path>` intentaba extracción SEMÁNTICA de docs/imágenes, que exige
# GEMINI/ANTHROPIC_API_KEY y fallaba en toda instancia sin key (visto en el
# VPS: 682 docs/imágenes). El grafo del harness es de CÓDIGO cross-repo
# ("¿quién consume este servicio?"), no semántico: code-only es lo correcto.
if graph_json >/dev/null; then
  graphify update repos >/dev/null 2>&1 \
    || { echo "⚠️  graphify update falló; fail-open, se reintenta en el próximo refresh" >&2; exit 0; }
else
  echo "→ grafo inicial de repos/ code-only (primera vez: puede tardar unos minutos)"
  graphify update repos >/dev/null 2>&1 \
    || { echo "⚠️  graphify build falló; fail-open" >&2; exit 0; }
fi
printf '%s' "$heads" > "$STAMP"
echo "✓ grafo al día ($(graph_json 2>/dev/null || echo 'graphify-out/'))"
