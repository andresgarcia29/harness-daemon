# slo-watchdog — investigación de alertas de burn-rate. Diseñado como
# receptor de webhook de Alertmanager (el manifiesto K8s lo expone como
# Job por alerta); como cron, barre las alertas activas.
# EL ROLLBACK NO ES DE ESTE JOB: Argo Rollouts aborta a stable solo.
JOB_NAME=slo-watchdog
JOB_TIER=medium
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(scripts/quiet.sh *),Bash(scripts/with-secrets.sh *),Bash(curl *)"

detect() {
  # Alertas activas de Alertmanager (URL en el entorno del CronJob)
  [ -n "${ALERTMANAGER_URL:-}" ] || { echo "define ALERTMANAGER_URL" >&2; return 3; }
  command -v jq >/dev/null || return 3
  curl -sf "$ALERTMANAGER_URL/api/v2/alerts?active=true&silenced=false" 2>/dev/null \
    | jq -r '.[] | select(.labels.severity=="critical" or (.labels.alertname|test("Burn|SLO"))) |
             "\(.labels.alertname) svc=\(.labels.service // .labels.job // "?") desde=\(.startsAt)"' \
    > "$FINDINGS" || return 1
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el slo-watchdog (investigador READ-ONLY, estilo
HolmesGPT). Por cada alerta de los hallazgos: (1) lee el runbook si
existe (docs/runbooks/<alertname>.md) y síguelo; (2) investiga con
herramientas de solo lectura vía scripts/with-secrets.sh y
scripts/quiet.sh: métricas (burn rate, ¿desde cuándo?), logs del
servicio, últimos deploys (tasks/*/ship.log + git log); (3) hipótesis
de causa raíz con evidencia; (4) si un deploy reciente correlaciona:
verifica si Argo Rollouts YA abortó a stable, y prepara el PR de
REVERT en git del commit sospechoso; (5) postea el diagnóstico como
issue P0/P1 y a Slack si hay webhook. NO ejecutes NADA mutante: ni
kubectl apply, ni rollback, ni silences — eso es del humano.'
