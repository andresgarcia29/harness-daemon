# ratchet-keeper — las métricas solo mejoran. High-water-marks en
# ratchets.json (committeado); el detector compara, el agente solo
# despierta para subir el piso o investigar una regresión.
JOB_NAME=ratchet-keeper
JOB_TIER=cheap
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(diff-cover *),Edit,Write"

detect() {
  command -v jq >/dev/null || return 3
  local rf="ratchets.json"
  [ -f "$rf" ] || { echo '{}' > "$rf"; }
  : > "$FINDINGS"
  for r in repos/*/; do
    [ -d "$r/.git" ] || continue
    local name; name="$(basename "$r")"
    # señal 1: supresiones de lint (solo pueden bajar)
    local sup=0
    [ -f "$r/eslint-suppressions.json" ] && sup=$(jq '[.. | numbers] | add // 0' "$r/eslint-suppressions.json" 2>/dev/null || echo 0)
    local noqa; noqa=$(grep -rc "# noqa" "$r" --include="*.py" 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
    local nolint; nolint=$(grep -rc "//nolint" "$r" --include="*.go" 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
    local total=$((sup + noqa + nolint))
    local prev; prev=$(jq -r --arg n "$name" '.[$n].suppressions // -1' "$rf")
    if [ "$prev" -ge 0 ] && [ "$total" -gt "$prev" ]; then
      echo "REGRESIÓN $name: supresiones $prev → $total (el ratchet solo baja)" >> "$FINDINGS"
    elif [ "$total" -lt "$prev" ] || [ "$prev" -lt 0 ]; then
      echo "BUMP $name: supresiones → $total (bajó o primer registro)" >> "$FINDINGS"
    fi
  done
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el ratchet-keeper. Hallazgos: (1) BUMP → actualiza
ratchets.json con el nuevo piso (mejor) y commitea directo con mensaje
"chore(ratchet): <repo> baja a N supresiones"; (2) REGRESIÓN → NO
toques el ratchet: identifica el commit que agregó supresiones nuevas
(git log -S "noqa"/-S "nolint") y abre issue asignado a su autor con
la lista exacta. El ratchet NUNCA se relaja desde este job — relajarlo
es una decisión humana con ADR.'
