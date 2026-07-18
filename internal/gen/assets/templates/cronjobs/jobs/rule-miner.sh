# rule-miner — mensual, el multiplicador: cada bug del mes se convierte
# en un detector determinista nuevo (regla semgrep/ast-grep). El sistema
# se vuelve más self-healing cada mes sin ampliar gasto en LLM.
JOB_NAME=rule-miner
JOB_TIER=expensive
JOB_MAX_TURNS=60
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(semgrep *),Bash(sg *),Edit,Write"

detect() {
  : > "$FINDINGS"
  for r in repos/*/; do
    [ -d "$r/.git" ] || continue
    local name; name="$(basename "$r")"
    git -C "$r" log --since="30 days ago" --grep="^fix" --grep="^revert" --oneline 2>/dev/null \
      | sed "s|^|$name |" >> "$FINDINGS"
  done
  # también: blocking repetidos en veredictos archivados
  grep -h '"blocking"' tasks/archive/*/verdict-*.json 2>/dev/null \
    | jq -r '.blocking[]? // empty' 2>/dev/null | sort | uniq -c | sort -rn | head -10 \
    | sed 's/^/veredicto-repetido: /' >> "$FINDINGS" || true
  local n; n=$(wc -l < "$FINDINGS" | tr -d ' ')
  [ "$n" -ge 5 ] && return 10 || return 0
}

JOB_PROMPT='Eres el rule-miner (patrón Autogrep/Getafix). Con los
commits fix/revert del mes y los blocking repetidos de review:
(1) lee los DIFFS de los 10 fixes más significativos (git show);
(2) identifica patrones generalizables: ¿qué forma tenía el bug ANTES
del fix? ¿aparece en más lugares?; (3) escribe reglas semgrep (en
semgrep/rules.yaml) o ast-grep que habrían ATRAPADO esos bugs, cada
una con mensaje de remediación concreto (el error del gate es un
prompt); (4) VALIDA cada regla corriéndola sobre el commit padre del
fix (debe disparar) y sobre main actual (no debe inundar de FPs — máx
5 hallazgos nuevos por regla); (5) un PR: "sensors: N reglas minadas
de los bugs de <mes>", con la evidencia de validación por regla. Regla
que no puedas validar = no entra.'
