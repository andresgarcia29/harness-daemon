# daily-digest — el reporte matutino: qué cambió, qué proponen los bots,
# cuánto costó la noche. Siempre despierta al agente (es su única función).
JOB_NAME=daily-digest
JOB_TIER=cheap
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(curl *),Write"

detect() {
  {
    echo "== commits de ayer por repo =="
    for r in repos/*/; do
      [ -d "$r/.git" ] || continue
      local log; log="$(git -C "$r" log --since=yesterday --oneline 2>/dev/null)"
      [ -n "$log" ] && { echo "--- $(basename "$r")"; echo "$log"; }
    done
    echo "== gasto de cronjobs (últimas 24h) =="
    if [ -f .cache/cron/ledger.jsonl ]; then
      tail -100 .cache/cron/ledger.jsonl | jq -rs '
        map(select(.ts >= (now-86400 | strftime("%Y-%m-%dT%H:%M:%SZ")))) |
        "total: $\(map(.cost_usd) | add // 0 | .*100 | round/100) · " +
        (group_by(.job) | map("\(.[0].job)=\(map(.status)|join(","))") | join(" · "))' 2>/dev/null
    fi
    echo "== beads cerrados ayer =="
    command -v bd >/dev/null && bd list --status closed --json 2>/dev/null | jq -r '.[] | .title' | head -20
  } > "$FINDINGS" 2>/dev/null
  return 10
}

JOB_PROMPT='Eres el daily-digest. Con los hallazgos redacta el digest
del día en docs/changelog/$(date +%Y-%m-%d).md: (1) qué se shippeó
(por repo, en lenguaje de producto, 1 línea c/u); (2) qué proponen los
bots (PRs abiertos por bot/* y renovate pendientes de humano); (3) el
costo de la noche y cualquier circuit breaker abierto; (4) máximo 30
líneas, sin relleno. Commitea el archivo directo (docs/ del meta-repo
no pasa por ship). Si hay webhook de Slack en $SLACK_WEBHOOK_URL,
postea el resumen con curl.'
