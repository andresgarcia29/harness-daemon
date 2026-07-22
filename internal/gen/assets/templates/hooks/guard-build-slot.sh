#!/usr/bin/env bash
# guard-build-slot.sh — PreToolUse (Bash): BLOQUEA `docker build`/`docker run` de
# toolchain sin envolver en el semáforo. Builds pesados paralelos funden la máquina
# compartida entre N sesiones (dato real: load 286 con 6 núcleos). Ley 8: todo build/run
# pesado pasa por scripts/build-slot.sh (serializa en el kernel, sin polling).
#
# Contrato Claude Code: exit 2 + stderr = bloquear la tool call (igual que
# guard-canonical.sh). Sin jq → exit 0 (fail-open, silencioso).
set -uo pipefail
input="$(cat)"
command -v jq >/dev/null 2>&1 || exit 0   # sin jq no bloqueamos (fail-open)

cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"
[ -z "$cmd" ] && exit 0

# Ya auto-serializado — el wrapper mismo (o algo que lo use por dentro) → no tocar.
case "$cmd" in
  *build-slot.sh*) exit 0 ;;
esac

# ¿`docker` seguido (en el MISMO comando, sin cruzar |, ; o &) de la palabra `build`
# o `run`? Cubre `docker build`, `docker buildx build`, `docker compose … build`,
# `docker run`. NO toca ps/logs/images/inspect/stop/kill/rm/exec/compose logs, etc.
if printf '%s' "$cmd" | grep -Eq 'docker[[:space:]]+([^[:space:]|&;]+[[:space:]]+)*(build|run)([[:space:]]|$)'; then
  echo "🚫 BLOQUEADO (Ley 8): 'docker build/run' pelado funde la máquina compartida entre sesiones (load 286 con 6 núcleos fue real)." >&2
  echo "→ Envuélvelo en el semáforo:  bash scripts/build-slot.sh docker <build|run> …" >&2
  echo "  Serializa cross-sesión con bloqueo de kernel (cero polling); límite max(1, núcleos/4), override HARNESS_BUILD_SLOTS." >&2
  exit 2
fi
exit 0
