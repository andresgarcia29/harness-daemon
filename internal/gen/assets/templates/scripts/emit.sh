#!/usr/bin/env bash
# emit.sh — el bus del harness. Lo que este sistema DECIDE y lo que NO deja hacer.
#
# Dos formas de usarlo:
#   source scripts/emit.sh        → te da la función `emit`
#   scripts/emit.sh <kind> <summary> [ok] [task]   → desde un comando o a mano
#
# POR QUÉ EXISTE: el panel sabía de agentes y tokens — todo eso lo presta Claude
# Code. Pero el harness NO SON SUS AGENTES: son sus decisiones y sus negativas.
# Los gates, las fases, los supuestos y las paradas son NUESTROS, y son la única
# capa que funciona igual con Claude Code, con Codex o con un humano. Sin esto,
# el panel enseña la mitad prestada y se calla la que importa.
#
# LEYES:
#   · FAIL-OPEN, siempre. Sale 0 pase lo que pase. Un bus de telemetría que
#     puede tumbar un ship es un bug, no una feature. Los que bloquean
#     (block-direct-push, guard-canonical) son fail-CLOSED a propósito.
#   · REDACTA ANTES DE ESCRIBIR. El resumen de un gate puede traer un comando
#     con un token. La ley de secretos también aplica al bus.
#   · APPEND-ONLY. Una línea JSON por evento. Cualquier script sabe hacer `>>`,
#     y el bus tiene que funcionar aunque el panel esté muerto.
#
# kinds: phase | gate | decision | assumption | stop | deploy | ship

set -u

_emit_ws() {
  # El workspace es el que tiene .harness/ o CLAUDE.md, subiendo desde aquí.
  local d="${CLAUDE_PROJECT_DIR:-${WS:-$PWD}}"
  printf '%s' "$d"
}

_emit_redact() {
  # OJO: nada de \b — el sed de macOS (BSD) no lo soporta y el patrón entero
  # deja de matchear EN SILENCIO: las llaves sk-/vault/slack/jwt viajaban al
  # bus sin redactar en Mac. Lo cachó tests/test_emit.sh. El borde de palabra
  # portable es (^|[^A-Za-z0-9_-]) conservando el prefijo con \1.
  sed -E \
    -e 's/(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}/[REDACTADO:gh]/g' \
    -e 's/(^|[^A-Za-z0-9_-])(hvs|hvb)\.[A-Za-z0-9_-]{20,}/\1[REDACTADO:vault]/g' \
    -e 's/(^|[^A-Za-z0-9_-])sk-[A-Za-z0-9_-]{20,}/\1[REDACTADO:key]/g' \
    -e 's/(^|[^A-Za-z0-9_-])xox[baprs]-[A-Za-z0-9-]{10,}/\1[REDACTADO:slack]/g' \
    -e 's/(^|[^A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}/\1[REDACTADO:jwt]/g' \
    -e 's/(AKIA|ASIA)[A-Z0-9]{12,}/[REDACTADO:aws]/g' \
    -e 's/lin_api_[A-Za-z0-9]{20,}/[REDACTADO:linear]/g' \
    -e 's/((password|passwd|secret|token|api_?key|authorization)["'"'"']?[[:space:]]*[:=][[:space:]]*["'"'"']?)[^"'"'"' ,}]{6,}/\1[REDACTADO]/gI'
}

# emit <kind> <summary> [ok] [task]
#   ok: true (pasó) | false (bloqueó) | vacío (no aplica)
emit() {
  local kind="${1:-}" summary="${2:-}" ok="${3:-}" task="${4:-${TASK:-}}"
  [ -n "$kind" ] || return 0
  local ws bus
  ws="$(_emit_ws)"; bus="$ws/.harness/events.jsonl"
  mkdir -p "$ws/.harness" 2>/dev/null || return 0
  command -v jq >/dev/null 2>&1 || return 0

  summary="$(printf '%s' "$summary" | _emit_redact | cut -c1-400)"
  local line
  if [ -n "$ok" ]; then
    line="$(jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg k "$kind" \
      --arg t "$task" --arg s "$summary" --arg a "${ACTOR:-${REPO:-harness}}" --argjson ok "$ok" \
      '{ts:$ts,kind:$k,task:$t,actor:$a,summary:$s,ok:$ok}' 2>/dev/null)"
  else
    line="$(jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg k "$kind" \
      --arg t "$task" --arg s "$summary" --arg a "${ACTOR:-${REPO:-harness}}" \
      '{ts:$ts,kind:$k,task:$t,actor:$a,summary:$s}' 2>/dev/null)"
  fi
  [ -n "$line" ] && printf '%s\n' "$line" >> "$bus" 2>/dev/null
  return 0
}

# Ejecutado directamente (no sourceado) → CLI. Lo usa /auto desde Bash.
# El :- importa: BASH_SOURCE no existe si te sourcean desde zsh o sh, y con
# `set -u` eso revienta el script que te sourceó. Un bus de telemetría que
# tumba a quien lo usa es exactamente lo que este archivo promete no ser.
if [ "${BASH_SOURCE[0]:-}" = "${0}" ]; then
  emit "$@"
  exit 0
fi
