#!/usr/bin/env bash
# pipeline-steps.sh: el motor DETERMINISTA de los pasos custom del pipeline.
#
# Un paso custom es un playbook `.claude/pipeline/<name>.md` (tuyo, instance-
# owned, nunca lo pisa el update) que cuelga de una fase. NO es una fase de
# policy: la máquina intake→archive es intocable. Este script decide qué
# pasos corren en una fase, en qué orden, y si un gate rojo para el pipeline.
# La lógica vive AQUÍ (testeable), no en la prosa de /auto.
#
# Uso:
#   pipeline-steps.sh list <task> <phase>   imprime, ordenados, los playbooks
#                                           con after:<phase> (el orquestador
#                                           los ejecuta uno por uno)
#   pipeline-steps.sh gate <task> <phase>   lee los resultados y GATEA:
#                                           gate:true + rojo → pause + exit 3
#
# Contrato de resultado: cada paso deja tasks/<id>/pipeline/<step>.json con
# {schema:1, ok:<bool>, summary, ...}. FAIL-CLOSED: ausente/inválido en un
# gate = rojo (un agente que se calla no pasa un gate). Esto es lo contrario
# del bus (emit.sh es fail-open a propósito: telemetría, no control).
#
# Portabilidad: bash 3.2, BSD userland, jq. Ids validados sin traversal.
set -u

CMD="${1:?uso: pipeline-steps.sh <list|gate> <task> <phase>}"
TASK="${2:?falta task-id}"
PHASE="${3:?falta phase}"
ok_id() { case "$1" in [A-Za-z0-9][A-Za-z0-9._-]*) return 0 ;; *) return 1 ;; esac; }
ok_id "$TASK" || { echo "❌ task-id inválido: '$TASK'" >&2; exit 1; }
case "$PHASE" in intake|rfc|implement|review|ship|deploy) ;; *) echo "❌ fase inválida: '$PHASE'" >&2; exit 1 ;; esac

WS="$(cd "$(dirname "$0")/.." && pwd)"
PDIR="$WS/.claude/pipeline"
RDIR="$WS/tasks/$TASK/pipeline"

# fm <archivo> <clave>: valor del frontmatter (entre los dos '---'), o "".
fm() {
  awk -v k="$2:" '
    NR==1 && $0!="---" { exit }
    NR>1 && $0=="---" { exit }
    NR>1 {
      line=$0; sub(/[ \t]*#.*$/, "", line)
      n=index(line, ":")
      if (n>0) {
        key=substr(line,1,n); val=substr(line,n+1)
        gsub(/[ \t]/,"",key); sub(/^[ \t]+/,"",val); sub(/[ \t]+$/,"",val)
        if (key==k) { print val; exit }
      }
    }
  ' "$1" 2>/dev/null
}

# ── list: los playbooks de esta fase, ordenados por (order, filename) ──
list_steps() {
  [ -d "$PDIR" ] || return 0
  local f after order name
  for f in "$PDIR"/*.md; do
    [ -f "$f" ] || continue
    after="$(fm "$f" after)"
    [ "$after" = "$PHASE" ] || continue
    order="$(fm "$f" order)"; case "$order" in ''|*[!0-9]*) order=100 ;; esac
    name="$(basename "$f")"
    printf '%s\t%s\t%s\n' "$order" "$name" "$f"
  done | sort -t"$(printf '\t')" -k1,1n -k2,2 | cut -f3
}

step_id() {  # step id = frontmatter `step:` o basename sin .md
  local s; s="$(fm "$1" step)"; [ -n "$s" ] || s="$(basename "$1" .md)"; printf '%s' "$s"
}

case "$CMD" in
  list)
    list_steps
    ;;
  gate)
    bad=0
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      sid="$(step_id "$f")"; ok_id "$sid" || continue
      gate="$(fm "$f" gate)"
      res="$RDIR/$sid.json"
      ok="false"; summary="sin resultado"
      if [ -f "$res" ] && jq -e '.ok==true' "$res" >/dev/null 2>&1; then
        ok="true"; summary="$(jq -r '.summary // "ok"' "$res" 2>/dev/null)"
      elif [ -f "$res" ]; then
        summary="$(jq -r '.summary // "rojo"' "$res" 2>/dev/null)"
      fi
      # el bus: gate → kind gate; advisory → kind decision (kinds que el panel ya renderiza)
      if [ -f "$WS/scripts/emit.sh" ]; then
        if [ "$gate" = "true" ]; then
          bash "$WS/scripts/emit.sh" gate "paso $sid: $summary" "$ok" "$TASK" 2>/dev/null || true
        else
          bash "$WS/scripts/emit.sh" decision "paso $sid (advisory): $summary" "" "$TASK" 2>/dev/null || true
        fi
      fi
      if [ "$gate" = "true" ] && [ "$ok" != "true" ]; then
        echo "⛔ paso custom '$sid' (gate) rojo en $PHASE: $summary"
        # registra la parada en la máquina de estados (razón cerrada)
        [ -f "$WS/tasks/$TASK/state.json" ] && python3 "$WS/scripts/harness-policy.py" \
          --policy "$WS/harness-policy.json" pause "$WS/tasks/$TASK" \
          --reason custom_step_failed --detail "$sid: $summary" --actor orchestrator 2>/dev/null || true
        bad=1
      fi
    done <<EOF
$(list_steps)
EOF
    if [ "$bad" -ne 0 ]; then
      echo "   ↳ remediación: arregla lo que el paso reporta, borra su result rojo"
      echo "     (tasks/$TASK/pipeline/), 'harness-policy.py resume' y re-corre /auto $TASK"
      exit 3
    fi
    ;;
  *)
    echo "❌ subcomando desconocido: $CMD (list|gate)" >&2; exit 1 ;;
esac
