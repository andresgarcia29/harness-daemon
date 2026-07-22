# skill-miner — mensual, hermano del rule-miner: donde rule-miner convierte
# bugs repetidos en reglas semgrep, este convierte PROCEDIMIENTOS repetidos
# en skills (.claude/skills/). Detector determinista: supuestos idénticos
# entre tareas, decisiones/paradas que se repiten en el bus. El agente solo
# despierta si hay repetición real, y todo aterriza como PR — merge = la
# ratificación humana que la ley exige.
JOB_NAME=skill-miner
JOB_TIER=expensive
JOB_MAX_TURNS=40
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(jq *),Write,Edit"

detect() {
  : > "$FINDINGS"
  # 1. supuestos idénticos en ≥3 tareas (mismo texto normalizado)
  cat tasks/*/assumptions.md tasks/archive/*/assumptions.md 2>/dev/null \
    | grep -E '^- SUPUESTO:' | tr 'A-Z' 'a-z' | sed 's/  */ /g' \
    | sort | uniq -c | awk '$1 >= 3' \
    | sed 's/^ *[0-9]* /supuesto-repetido: /' >> "$FINDINGS" || true
  # 2. decisiones y paradas que se repiten en el bus (≥3 veces)
  if [ -f .harness/events.jsonl ]; then
    jq -r 'select(.kind == "decision" or .kind == "stop") | .text' \
      .harness/events.jsonl 2>/dev/null | tr 'A-Z' 'a-z' \
      | sort | uniq -c | awk '$1 >= 3' \
      | sed 's/^ *[0-9]* /evento-repetido: /' >> "$FINDINGS" || true
  fi
  local n; n=$(wc -l < "$FINDINGS" | tr -d ' ')
  [ "$n" -ge 2 ] && return 10 || return 0
}

JOB_PROMPT='Eres el skill-miner. Los hallazgos son procedimientos que el
harness repite sin tenerlos empaquetados. Sigue la guía de
.claude/skills/skill-creator/SKILL.md al pie de la letra:
(1) por cada patrón repetido, decide con SU tabla si es skill,
regla semgrep (déjasela al rule-miner), ADR o script — solo las skills
son tuyas; (2) para cada skill: rastrea la evidencia real (qué tareas,
qué se re-explicó) leyendo tasks/ y el bus; (3) escribe
.claude/skills/<nombre>/SKILL.md con la anatomía de la guía —
description con palabras-gatillo reales, comandos EXACTOS de este
workspace, corta; (4) máximo 3 skills por corrida: mejor 1 buena que 5
plausibles; (5) UN PR: "skills: N procedimientos repetidos empaquetados
(<mes>)" citando la evidencia por skill. El merge del humano es la
ratificación — jamás toques main.'
