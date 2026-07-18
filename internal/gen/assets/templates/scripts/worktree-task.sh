#!/usr/bin/env bash
# worktree-task.sh — una tarea = un worktree por repo. Nunca el clon base.
#
# Uso:
#   worktree-task.sh <task-id> <repo> [repo...]   crea worktrees de la tarea
#   worktree-task.sh --rm <task-id>               quita los worktrees (post-ship)
set -euo pipefail
WS="$(cd "$(dirname "$0")/.." && pwd)"

if [ "${1:-}" = "--rm" ]; then
  TASK="${2:?uso: worktree-task.sh --rm <task-id>}"
  for wt in "$WS/worktrees/$TASK"/*/; do
    [ -d "$wt" ] || continue
    repo="$(basename "$wt")"
    if [ -n "$(git -C "$wt" status --porcelain 2>/dev/null)" ]; then
      echo "⚠️  $wt tiene cambios sin commitear — NO lo quito. Shippea o descarta primero."
      continue
    fi
    git -C "$WS/repos/$repo" worktree remove "$wt" && echo "🧹 removido: $wt"
    git -C "$WS/repos/$repo" branch -D "task/$TASK" 2>/dev/null || true
  done
  rmdir "$WS/worktrees/$TASK" 2>/dev/null || true
  exit 0
fi

TASK="${1:?uso: worktree-task.sh <task-id> <repo> [repo...]}"; shift
[ $# -gt 0 ] || { echo "❌ indica al menos un repo"; exit 1; }

for repo in "$@"; do
  base="$WS/repos/$repo"
  wt="$WS/worktrees/$TASK/$repo"
  [ -d "$base/.git" ] || { echo "❌ repo desconocido: $repo (ver manifest.yaml)"; exit 1; }
  [ -d "$wt" ] && { echo "→ ya existe: $wt"; continue; }
  mkdir -p "$(dirname "$wt")"
  git -C "$base" fetch origin
  git -C "$base" worktree add -b "task/$TASK" "$wt" origin/main 2>/dev/null \
    || git -C "$base" worktree add "$wt" "task/$TASK"
  echo "✅ worktree: $wt (rama task/$TASK)"
done

mkdir -p "$WS/tasks/$TASK"
echo "→ artefactos de la tarea (task.md, plan.md, verdict-*.json) en $WS/tasks/$TASK/"
