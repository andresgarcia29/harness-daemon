#!/usr/bin/env bash
# Hook PreToolUse (Bash) — LEY 1: push a main SOLO vía scripts/ship.sh.
# Bloquea (exit 2) cualquier `git push` que apunte a main. ship.sh no
# matchea aquí porque el comando del tool es `scripts/ship.sh ...`.
# FAIL-CLOSED: sin jq, cualquier git push se bloquea (mejor molesto que agujero).
set -uo pipefail
input="$(cat)"

if ! command -v jq >/dev/null 2>&1; then
  if printf '%s' "$input" | grep -q "git push"; then
    echo "⛔ jq no disponible: bloqueo git push por precaución. Instala jq o usa scripts/ship.sh" >&2
    exit 2
  fi
  exit 0
fi

cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"
case "$cmd" in
  *"git push"*) ;;
  *) exit 0 ;;
esac

# push explícito a main (o HEAD:main, o cualquier ref:main) → bloqueado
if printf '%s' "$cmd" | grep -qE 'git push[^|;&]*[[:space:]](main|[^ ]*:main)([[:space:]]|$)'; then
  echo "⛔ push directo a main bloqueado (Ley 1). Única puerta: scripts/ship.sh <task-id> <repo>" >&2
  exit 2
fi
# push sin refspec: bloqueado si no estamos en una rama task/*
if printf '%s' "$cmd" | grep -qE 'git push([[:space:]]+(origin|--[a-z-]+))*[[:space:]]*$'; then
  echo "⛔ git push sin refspec bloqueado (Ley 1): podría apuntar a main. Pushea ramas task/* explícitas o usa scripts/ship.sh" >&2
  exit 2
fi
exit 0
