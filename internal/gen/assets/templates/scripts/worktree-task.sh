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
  # Si ya no queda ningún worktree de repo, borra el dir de la tarea. rmdir no basta:
  # gowork.sh/py.sh (go.work, shims) dejan debris fuera de los worktrees y el rmdir
  # no-recursivo falla. Sólo rm -rf si no sobrevive un worktree con trabajo sin shippear.
  if ! ls -d "$WS/worktrees/$TASK"/*/ >/dev/null 2>&1; then
    rm -rf "$WS/worktrees/$TASK"
  else
    echo "→ quedan worktrees en $WS/worktrees/$TASK — no borro el dir de la tarea."
  fi
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
  # Refresca el clon canónico ANTES de crear el worktree: los worktrees nacen frescos de
  # origin/main, pero repos/<repo> queda stale y todo lo que compone contra el canónico
  # (shims de py.sh, fallback de gowork.sh, verifies) se rompe silencioso. Best-effort:
  # offline o dirty NO bloquea — el worktree nace de origin/main igual gracias al fetch.
  cur="$(git -C "$base" symbolic-ref --short HEAD 2>/dev/null || true)"
  if [ "$cur" = "main" ] && [ -z "$(git -C "$base" status --porcelain 2>/dev/null)" ]; then
    git -C "$base" pull --ff-only origin main >/dev/null 2>&1 \
      || echo "⚠️  no pude refrescar repos/$repo (offline o divergió) — sigo; el worktree nace de origin/main."
  else
    echo "⚠️  repos/$repo no está limpio en main${cur:+ (rama: $cur)} — no lo refresco (el worktree nace de origin/main igual)."
  fi
  git -C "$base" worktree add -b "task/$TASK" "$wt" origin/main 2>/dev/null \
    || git -C "$base" worktree add "$wt" "task/$TASK"
  echo "✅ worktree: $wt (rama task/$TASK)"
done

# Loop interno nativo de Go: regenera el go.work de la tarea (worktree ∪ canónico como
# fallback). Best-effort — no-op limpio si no hay módulos Go (Ley 9).
bash "$WS/scripts/gowork.sh" "$TASK" >/dev/null 2>&1 || true

mkdir -p "$WS/tasks/$TASK"
echo "→ artefactos de la tarea (task.md, plan.md, verdict-*.json) en $WS/tasks/$TASK/"
