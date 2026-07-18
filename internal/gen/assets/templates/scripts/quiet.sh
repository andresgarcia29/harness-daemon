#!/usr/bin/env bash
# quiet.sh — economía de tokens en CLIs ruidosos (kubectl logs, gh run view,
# gcloud, docker). Si el output supera QUIET_MAX_LINES muestra head+tail y
# guarda el dump COMPLETO en .cache/quiet/ (léelo bajo demanda).
# Compone: quiet.sh with-secrets.sh kubectl logs ...
#
# REGLA DE ORO: truncar es economía; truncar EL ERROR es autolesión. El output
# de un fallo no es ruido, es la señal con la que el agente se corrige — si la
# borramos, ahorramos tokens y pagamos iteraciones. Por eso, además de head y
# tail, SIEMPRE se rescatan las líneas de error del medio, y el marcador dice
# cuántas se omitieron: un contexto lossy debe SABERSE lossy, o el modelo
# infiere que la corrida salió limpia.
set -uo pipefail
MAX="${QUIET_MAX_LINES:-120}"; NHEAD="${QUIET_HEAD:-40}"; NTAIL="${QUIET_TAIL:-40}"
WS="$(cd "$(dirname "$0")/.." && pwd)"
CACHE="$WS/.cache/quiet"; mkdir -p "$CACHE"

[ $# -gt 0 ] || { echo "uso: quiet.sh <comando...>"; exit 1; }
slug="$(printf '%s' "$*" | tr -c 'A-Za-z0-9' '-' | cut -c1-48)"
out="$CACHE/$(date +%Y%m%d-%H%M%S)-$slug.log"

set +e; "$@" > "$out" 2>&1; rc=$?; set -e
lines=$(wc -l < "$out" | tr -d ' ')

ERRPAT="${QUIET_ERROR_PATTERN:-Error|ERROR|Traceback|Exception|FAILED|FAIL:|assert|panic:|fatal|Fatal|undefined reference|cannot find|not found|✗|✘|✖}"

if [ "$lines" -le "$MAX" ]; then
  cat "$out"; rm -f "$out"
else
  elided=$((lines - NHEAD - NTAIL))
  head -n "$NHEAD" "$out"
  # Rescate: líneas de error que caen en el tramo omitido. Sin esto, un
  # stack trace en el minuto 3 de un log de 4000 líneas desaparece y el
  # agente "arregla" a ciegas.
  errs="$(awk -v a="$((NHEAD + 1))" -v b="$((lines - NTAIL))" -v pat="$ERRPAT" \
              -v max="${QUIET_ERROR_MAX:-15}" '
          NR >= a && NR <= b && $0 ~ pat { printf "  L%d: %s\n", NR, $0; if (++c >= max) exit }
        ' "$out" 2>/dev/null)"
  echo "··· [$elided líneas omitidas — dump completo: $out] ···"
  if [ -n "$errs" ]; then
    echo "··· [errores rescatados del tramo omitido — el resto es ruido, esto no:] ···"
    printf '%s\n' "$errs"
    echo "··· [fin del rescate] ···"
  fi
  tail -n "$NTAIL" "$out"
fi
exit "$rc"
