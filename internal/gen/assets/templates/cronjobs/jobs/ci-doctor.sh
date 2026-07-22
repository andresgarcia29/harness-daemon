# ci-doctor — triage de builds rojos. Ideal como trigger de workflow_run
# on failure; como cron (cada 30min) barre los runs fallidos recientes.
JOB_NAME=ci-doctor
JOB_TIER=medium
JOB_TOOLS="Read,Grep,Glob,Bash(gh *),Bash(git *),Edit,Write"

detect() {
  command -v gh >/dev/null || return 3
  command -v jq >/dev/null || return 3
  local found=0
  while read -r repo; do
    [ -d "repos/$repo/.git" ] || continue
    local slug; slug="$(git -C "repos/$repo" remote get-url origin 2>/dev/null | sed -E 's#.*[:/]([^/]+/[^/.]+)(\.git)?$#\1#')"
    [ -n "$slug" ] || continue
    gh run list --repo "$slug" --branch main --status failure \
      --created "$(date -u -v-2H +%Y-%m-%dT%H:%M 2>/dev/null || date -u -d '2 hours ago' +%Y-%m-%dT%H:%M)" \
      --json databaseId,displayTitle,workflowName --jq \
      '.[] | "'"$repo"' run=\(.databaseId) [\(.workflowName)] \(.displayTitle)"' 2>/dev/null \
      | tee -a "$FINDINGS" | grep -q . && found=1
  done < <(ls repos/ 2>/dev/null)
  [ "$found" -eq 1 ] && return 10 || return 0
}

JOB_PROMPT='Eres el ci-doctor del harness. Por cada run fallido de los
hallazgos: (1) lee el log de fallo con `gh run view <id> --repo <slug>
--log-failed` (vía scripts/quiet.sh si es largo) y EXTRAE TODAS las
causas de una vez (grep de FAIL/❌/error sobre el log completo): un run
rojo casi nunca tiene una sola, y arreglar de a una quema un ciclo de
CI por causa; (1b) OJO con el entorno: el runner es ubuntu (sh=dash,
/tmp plano, sin homebrew): un fix que solo probaste en tu entorno no
está probado; (2) clasifica: flaky
(re-lanza el run UNA vez y anota al flake-warden) · infra (OOM/timeout:
issue con diagnóstico) · rotura real (fix quirúrgico mínimo en rama
bot/, PR; si el fix no es obvio en 2 intentos, PR de REVERT del commit
culpable con el diagnóstico). Nunca desactives tests para poner el
build en verde.'
