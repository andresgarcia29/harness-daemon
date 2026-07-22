#!/usr/bin/env bash
# minion-probe.sh: el patrón MinionS de verdad (arXiv:2502.15964), como script.
# NO es elegir un modelo más barato. Es DESCOMPONER + PARALELIZAR bajo un
# supervisor:
#
#   1. el SUPERVISOR (deep) descompone la tarea en sub-preguntas ACOTADAS y
#      con scope (esto lo hace el agente y es la parte inteligente);
#   2. este script hace FAN-OUT: un WORKER barato (tier `worker`) responde
#      CADA sub-pregunta EN PARALELO, viendo SOLO su trozo de contexto;
#   3. el supervisor SINTETIZA sobre las respuestas citadas, gastando sus
#      tokens caros en JUICIO, no en lectura.
#
# El ahorro de tokens/dinero/tiempo es CONSECUENCIA de descomponer bien, no
# el objetivo. El objetivo: calidad de frontier con throughput de muchos
# workers dirigidos.
#
# Uso: minion-probe.sh <task-id> <probes.json>
#   probes.json = [ {"id":"...", "q":"...", "scope":{...}}, ... ]
#     scope.files  = ["ruta", ...]        el worker ve esos archivos
#     scope.repo   = "atlas"              el worker ve el brief del repo
#     scope.graph  = "consulta"           el worker ve el resultado del grafo
#   (un probe sin scope resoluble se responde "SIN CONTEXTO": honesto, no
#    inventa.)
#
# Escribe tasks/<id>/probes/<qid>.md por respuesta (citada) e imprime el pack
# combinado en stdout. Fail-open: sin worker/claude/jq imprime los probes en
# crudo para que el supervisor los responda él mismo. $0 con stamp por hash.
# Portabilidad: bash 3.2, BSD userland. Cap de paralelismo = min(8, núcleos).
set -u

TASK="${1:?uso: minion-probe.sh <task-id> <probes.json>}"
PROBES="${2:?uso: minion-probe.sh <task-id> <probes.json>}"
ok_id() { case "$1" in [A-Za-z0-9][A-Za-z0-9._-]*) return 0 ;; *) return 1 ;; esac; }
ok_id "$TASK" || { echo "❌ task-id inválido: '$TASK'"; exit 1; }
[ -f "$PROBES" ] || { echo "❌ no existe $PROBES"; exit 1; }

WS="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$WS/tasks/$TASK/probes"
mkdir -p "$OUT_DIR"

command -v jq >/dev/null || { echo "⚠️  sin jq: devuelvo los probes crudos (fail-open)"; cat "$PROBES"; exit 0; }
jq -e 'type == "array"' "$PROBES" >/dev/null 2>&1 || { echo "❌ probes.json debe ser un array"; exit 1; }

worker_id=""
[ -x "$WS/scripts/stamp-models.sh" ] && worker_id="$(bash "$WS/scripts/stamp-models.sh" resolve worker 2>/dev/null || true)"

# Cap de paralelismo (mismo criterio que los gates de ship y el DAG).
cores="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)"
CAP=$(( cores < 8 ? cores : 8 )); [ "$CAP" -lt 1 ] && CAP=1

# ── resuelve el scope de UN probe a texto (solo su trozo, nada más) ──
scope_text() {  # scope_text <probe-json>
  local p="$1" kind files repo gq
  kind="$(printf '%s' "$p" | jq -r '.scope | keys[0] // "none"' 2>/dev/null)"
  case "$kind" in
    files)
      printf '%s' "$p" | jq -r '.scope.files[]?' 2>/dev/null | while IFS= read -r f; do
        [ -f "$WS/$f" ] && { echo "### $f"; cat "$WS/$f"; echo; }
      done ;;
    repo)
      repo="$(printf '%s' "$p" | jq -r '.scope.repo' 2>/dev/null)"
      if ok_id "$repo"; then
        [ -x "$WS/scripts/repo-brief.sh" ] && bash "$WS/scripts/repo-brief.sh" "$repo" >/dev/null 2>&1
        [ -f "$WS/.cache/briefs/$repo.md" ] && cat "$WS/.cache/briefs/$repo.md"
      fi ;;
    graph)
      gq="$(printf '%s' "$p" | jq -r '.scope.graph' 2>/dev/null)"
      command -v graphify >/dev/null 2>&1 && graphify query "$gq" 2>/dev/null | head -80 ;;
    *) : ;;
  esac
}

# ── un worker responde UN probe sobre SU scope (o SIN CONTEXTO) ──
answer_one() {  # answer_one <probe-json>
  local p="$1" qid q ctx out
  qid="$(printf '%s' "$p" | jq -r '.id' 2>/dev/null)"
  q="$(printf '%s' "$p" | jq -r '.q' 2>/dev/null)"
  ok_id "$qid" || return 0
  out="$OUT_DIR/$qid.md"
  ctx="$(scope_text "$p")"

  if [ -z "$worker_id" ] || ! command -v claude >/dev/null 2>&1 || [ -z "$ctx" ]; then
    # fail-open / sin contexto: honesto, el supervisor decide
    { echo "## $qid: $q"
      [ -z "$ctx" ] && echo "SIN CONTEXTO (scope vacío o no resoluble): respóndela tú, supervisor." \
                    || { echo "(sin worker; contexto crudo:)"; printf '%s\n' "$ctx"; }
    } > "$out"
    cat "$out"; return 0
  fi

  local prompt="Eres un WORKER MinionS. Responde SOLO esta pregunta desde el
material dado. NO opines de más, NO decidas: extrae el hecho, CÍTALO como
\`archivo:línea\`, y si el material no la responde di exactamente 'DESCONOCIDO'
(el supervisor tirará de la fuente). Máxima densidad, viñetas.

PREGUNTA: $q

MATERIAL:
$ctx"
  local ans; ans="$(printf '%s' "$prompt" | claude -p --model "$worker_id" 2>/dev/null || true)"
  [ -n "$ans" ] || ans="DESCONOCIDO (el worker no respondió)"
  { echo "## $qid: $q"; printf '%s\n' "$ans"; } > "$out"
  cat "$out"
}

# ── fan-out con cap de paralelismo, bash 3.2-safe (sin wait -n ni parallel) ──
# Pool FIFO: cuando hay CAP corriendo, espera al MÁS VIEJO antes de lanzar otro.
pids=""
n="$(jq 'length' "$PROBES")"
i=0
while [ "$i" -lt "$n" ]; do
  probe="$(jq -c ".[$i]" "$PROBES")"
  answer_one "$probe" &
  pids="$pids $!"
  i=$((i+1))
  # shellcheck disable=SC2086
  set -- $pids
  if [ "$#" -ge "$CAP" ]; then
    wait "$1" 2>/dev/null || true
    shift
    pids="$*"
  fi
done
wait

echo "─── pack de respuestas (el supervisor sintetiza el plan sobre esto) ───"
echo "_Cada respuesta cita su fuente; 'DESCONOCIDO'/'SIN CONTEXTO' = tira de la fuente cruda._"
