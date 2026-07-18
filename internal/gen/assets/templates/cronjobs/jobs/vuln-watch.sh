# vuln-watch — vulnerabilidades nuevas en dependencias (diff contra ayer).
JOB_NAME=vuln-watch
JOB_TIER=medium
JOB_TOOLS="Read,Grep,Glob,Bash(gh *),Bash(git *),Bash(osv-scanner *),Bash(npm *),Bash(go *),Bash(uv *),Edit,Write"

detect() {
  command -v osv-scanner >/dev/null || return 3
  command -v jq >/dev/null || return 3
  local today="$FINDINGS.today" prev=".cache/cron/vuln-watch.baseline"
  osv-scanner scan --recursive repos/ --format json 2>/dev/null \
    | jq -r '.results[]?.packages[]? | .package.name as $p | .vulnerabilities[]? | "\(.id) \($p)"' \
    | sort -u > "$today" || true
  if [ -f "$prev" ]; then
    comm -13 "$prev" "$today" > "$FINDINGS"
  else
    cp "$today" "$FINDINGS"
  fi
  cp "$today" "$prev"; rm -f "$today"
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el vuln-watch. Por cada vulnerabilidad NUEVA de los
hallazgos (formato: ID paquete): (1) verifica si hay versión con fix
(osv.dev); (2) si la hay: prepara el bump en una rama bot/, corre los
tests del repo afectado, abre PR titulado "fix(security): <ID>";
(3) si NO hay fix: issue con el ID, el paquete, qué código nuestro lo
usa (grep de imports) y mitigación posible. Prioriza por severidad.
Un PR por vulnerabilidad, no lotes.'
