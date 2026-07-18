#!/usr/bin/env bash
# Hook PreToolUse (Edit|Write|MultiEdit) — dos leyes con dientes:
#   LEY 4: nunca editar el clon canónico repos/<x>; el trabajo vive en
#          worktrees/<task>/<repo>.
#   LEY 0: NINGÚN agente edita el harness mientras hace una tarea. Los gates,
#          los hooks y los denials NO son código de producto: son la ley que
#          juzga al agente que los tocaría. Un agente atascado en el gate 6 al
#          que se le ocurre "arreglar" ship.sh no está pasando el gate — lo
#          está borrando, y con él todos los demás para siempre. Cambiar la ley
#          es trabajo de un humano, en un PR, mirándolo de frente.
# FAIL-CLOSED: sin jq, bloquea por precaución.
set -uo pipefail
input="$(cat)"

if ! command -v jq >/dev/null 2>&1; then
  if printf '%s' "$input" | grep -qE "/repos/|/\.claude/hooks/|ship\.sh|settings\.json"; then
    echo "⛔ jq no disponible: bloqueo por precaución (Ley 4 / Ley 0). Instala jq." >&2
    exit 2
  fi
  exit 0
fi

path="$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')"
[ -n "$path" ] || exit 0

root="${CLAUDE_PROJECT_DIR:-$(pwd)}"
case "$path" in
  "$root"/repos/*)
    echo "⛔ edición del clon canónico bloqueada (Ley 4): $path" >&2
    echo "   Crea tu worktree: scripts/worktree-task.sh <task-id> <repo> y edita ahí." >&2
    exit 2
    ;;
esac
exit 0
