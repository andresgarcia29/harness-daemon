# dep-shepherd — pastor de dependencias. Renovate (self-hosted) crea los
# PRs con minimumReleaseAge:14d y automergea patches con CI verde; este
# job revisa lo que Renovate NO automergea (majors, confianza baja).
JOB_NAME=dep-shepherd
JOB_TIER=medium
JOB_TOOLS="Read,Grep,Glob,Bash(gh *),Bash(git *),Bash(npm *),Bash(go *),Bash(uv *)"

detect() {
  command -v gh >/dev/null || return 3
  local found=0
  while read -r repo; do
    [ -d "repos/$repo/.git" ] || continue
    local slug; slug="$(git -C "repos/$repo" remote get-url origin 2>/dev/null | sed -E 's#.*[:/]([^/]+/[^/.]+)(\.git)?$#\1#')"
    [ -n "$slug" ] || continue
    gh pr list --repo "$slug" --author app/renovate --state open \
      --json number,title --jq '.[] | "'"$repo"' pr=\(.number) \(.title)"' 2>/dev/null \
      | tee -a "$FINDINGS" | grep -q . && found=1
  done < <(ls repos/ 2>/dev/null)
  [ "$found" -eq 1 ] && return 10 || return 0
}

JOB_PROMPT='Eres el dep-shepherd. Por cada PR de Renovate abierto:
(1) clasifica el riesgo: ¿major? ¿toca crypto/auth/serialización?;
(2) para riesgo alto: grepa los IMPORTS reales del paquete en el repo,
lee el changelog de breaking changes (context7/URL del release), y
verifica que CI verde no esconda una opción renombrada o un default
cambiado ("CI verde no es suficiente"); (3) postea tu matriz de riesgo
como comentario del PR; (4) si es seguro y CI está verde, aprueba y
mergea con `gh pr merge --squash`; si requiere cambios, commitea el fix
de compatibilidad EN el PR de Renovate. Nunca mergees un major sin
revisar imports.'
