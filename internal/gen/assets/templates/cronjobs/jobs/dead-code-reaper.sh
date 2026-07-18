# dead-code-reaper — código muerto por lenguaje (semanal). Whitelists
# committeadas en .cache/cron/deadcode-whitelist-<repo>.txt para FPs.
JOB_NAME=dead-code-reaper
JOB_TIER=medium
JOB_MAX_TURNS=60
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(go *),Bash(npm *),Bash(npx *),Bash(uv *),Bash(knip *),Bash(vulture *),Bash(deadcode *),Edit,Write"

detect() {
  local found=0
  for r in repos/*/; do
    [ -d "$r/.git" ] || continue
    local name; name="$(basename "$r")"
    local wl=".cache/cron/deadcode-whitelist-$name.txt"; touch "$wl"
    {
      if [ -f "$r/go.mod" ] && command -v deadcode >/dev/null; then
        (cd "$r" && deadcode ./... 2>/dev/null)
      fi
      if [ -f "$r/package.json" ] && command -v knip >/dev/null; then
        (cd "$r" && knip --no-exit-code --reporter compact 2>/dev/null)
      fi
      if [ -f "$r/pyproject.toml" ] && command -v vulture >/dev/null; then
        (cd "$r" && vulture . --min-confidence 90 2>/dev/null)
      fi
    } | grep -vxF -f "$wl" | sed "s|^|$name: |" >> "$FINDINGS"
  done
  [ -s "$FINDINGS" ] && found=1
  command -v deadcode >/dev/null || command -v knip >/dev/null || command -v vulture >/dev/null || return 3
  [ "$found" -eq 1 ] && return 10 || return 0
}

JOB_PROMPT='Eres el dead-code-reaper. Con los hallazgos de código
muerto: (1) agrupa por repo; (2) borra en LOTES PEQUEÑOS (≤10 símbolos
por commit), corriendo la suite del repo tras cada lote; (3) si un
hallazgo es falso positivo (dispatch dinámico, reflexión, API pública),
NO lo borres: agrégalo a la whitelist .cache/cron/deadcode-whitelist-
<repo>.txt con comentario; (4) un PR por repo con el log de qué se
borró y la evidencia de tests verdes. Ante la duda, whitelist, no
borrado — el reaper es conservador.'
