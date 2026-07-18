#!/usr/bin/env bash
# ui-emit.sh — el bus de eventos del harness. Traduce hooks de Claude Code
# a líneas de .harness/events.jsonl, que es lo que lee la UI (make ui).
#
# LEY DE ESTE HOOK: es un OBSERVADOR. Jamás bloquea, jamás decide, jamás
# escribe en stdout. Sale 0 SIEMPRE — incluso roto. Un hook de telemetría
# que puede tumbar el pipeline es un bug, no una feature: los hooks que
# BLOQUEAN (block-direct-push, guard-canonical) son fail-CLOSED a
# propósito; este es fail-OPEN por la razón opuesta.
#
# Se registra en PostToolUse (no Pre: no queremos latencia antes de cada
# tool), SubagentStop, Stop, SessionStart y UserPromptSubmit.
set -u

BUS_DIR="${CLAUDE_PROJECT_DIR:-$PWD}/.harness"
BUS="$BUS_DIR/events.jsonl"
MAX_BYTES=5242880   # 5 MB → rota. Sin server corriendo esto crece igual.

exit_ok() { exit 0; }
trap exit_ok EXIT
command -v jq >/dev/null 2>&1 || exit 0

mkdir -p "$BUS_DIR" 2>/dev/null || exit 0

# Rotación barata: un stat por llamada, portable macOS/Linux.
size=$(stat -f%z "$BUS" 2>/dev/null || stat -c%s "$BUS" 2>/dev/null || echo 0)
[ "${size:-0}" -gt "$MAX_BYTES" ] && mv -f "$BUS" "$BUS.1" 2>/dev/null

payload="$(cat 2>/dev/null)"
[ -n "$payload" ] || exit 0

KIND="${1:-tool}"

# REDACCIÓN (ley de secretos: los valores no van al repo, al chat NI a la
# UI). El summary es lo único que se muestra; se trunca y se tapan las
# formas de secreto más comunes ANTES de tocar el disco.
redact() {
  sed -E \
    -e 's/(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}/[REDACTADO:gh]/g' \
    -e 's/\b(hvs|hvb)\.[A-Za-z0-9_-]{20,}/[REDACTADO:vault]/g' \
    -e 's/\bsk-[A-Za-z0-9_-]{20,}/[REDACTADO:key]/g' \
    -e 's/\bxox[baprs]-[A-Za-z0-9-]{10,}/[REDACTADO:slack]/g' \
    -e 's/\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}/[REDACTADO:jwt]/g' \
    -e 's/(AKIA|ASIA)[A-Z0-9]{12,}/[REDACTADO:aws]/g' \
    -e 's/lin_api_[A-Za-z0-9]{20,}/[REDACTADO:linear]/g' \
    -e 's/(-----BEGIN [A-Z ]*PRIVATE KEY-----)/[REDACTADO:privkey]/g' \
    -e 's/((password|passwd|secret|token|api_?key|authorization)["'"'"']?\s*[:=]\s*["'"'"']?)[^"'"'"' ,}]{6,}/\1[REDACTADO]/gI'
}

emit() { printf '%s\n' "$1" >> "$BUS" 2>/dev/null; }

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# La tarea se DERIVA del cwd (worktrees/<task>/<repo>), nunca de un archivo
# compartido: con diez sesiones abiertas, un .harness/current-task global se
# pisa entre sesiones y etiqueta los eventos con la tarea equivocada. Sin estado
# compartido no hay estado que corromper.
cwd="$(printf '%s' "$payload" | jq -r '.cwd // ""' 2>/dev/null)"
case "$cwd" in
  */worktrees/*) task="$(printf '%s' "${cwd#*/worktrees/}" | cut -d/ -f1)" ;;
  *) task="" ;;
esac

case "$KIND" in
  tool)
    line="$(printf '%s' "$payload" | jq -c --arg ts "$ts" --arg task "${task:-}" '
      {ts: $ts, kind: "tool", task: $task,
       session: (.session_id // ""),
       agent: (.agent_id // "main"),
       tool: (.tool_name // "?"),
       summary: (
         (.tool_input.command // .tool_input.file_path // .tool_input.pattern //
          .tool_input.description // .tool_input.prompt // "") | tostring | .[0:200]),
       ok: ((.tool_response.success // true) | tostring)}' 2>/dev/null | redact)"
    ;;
  subagent-start|subagent-stop|stop|session-start|prompt)
    line="$(printf '%s' "$payload" | jq -c --arg ts "$ts" --arg k "$KIND" --arg task "${task:-}" '
      {ts: $ts, kind: $k, task: $task,
       session: (.session_id // ""),
       agent: (.agent_id // "main"),
       summary: ((.prompt // .reason // "") | tostring | .[0:200])}' 2>/dev/null | redact)"
    ;;
  *) exit 0 ;;
esac

[ -n "${line:-}" ] && emit "$line"
exit 0
