#!/usr/bin/env bash
# fe.sh — corre un comando del package manager de un repo hijo frontend con las toolchains
# que garantiza scripts/bootstrap.sh en el PATH (node/bun). Espejo de scripts/py.sh
# (Python/uv) y scripts/gowork.sh (Go) para el mundo frontend. Es LOOP INTERNO NATIVO:
# itera con la toolchain del host, no en el contenedor.
#
# El package manager se AUTO-DETECTA por el lockfile del repo (universal, sin hardcodear cliente):
#   bun.lock / bun.lockb   → bun   (binario del PATH)
#   pnpm-lock.yaml         → pnpm  (vía corepack de node, cache local al workspace)
#   yarn.lock              → yarn  (vía corepack de node, cache local al workspace)
#   package-lock.json / —  → npm   (de node)
#
# Uso:
#   make fe CMD='install'                          # repo autodescubierto si hay uno solo, canónico
#   make fe CMD='install' REPO=<repo>             # otro repo, canónico
#   make fe CMD='run build' REPO=<repo> TASK=<id> # corre en el worktree de esa tarea (Ley 4)
#   bash scripts/fe.sh '<cmd>' [REPO] [TASK]
set -euo pipefail
WS="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"; cd "$WS"
REPOS_DIR="$WS/repos"
WT_DIR="$WS/worktrees"

CMD="${1:-}"
REPO="${2:-}"
TASK="${3:-}"

[ -n "$CMD" ] || { echo "❌ falta CMD — uso: make fe CMD='install' [REPO=<repo>] [TASK=<id>]"; exit 2; }

# node siempre necesario (trae npm y corepack para pnpm/yarn); bun se exige sólo si el repo lo usa.
command -v node >/dev/null 2>&1 || { echo "❌ node no está en el PATH — corre 'bash scripts/bootstrap.sh'"; exit 1; }

# ── REPO por defecto: autodescubrir candidatos (dirs de 1er/2do nivel bajo repos/ con package.json) ──
if [ -z "$REPO" ]; then
  cands=""
  for d in "$REPOS_DIR"/*/ "$REPOS_DIR"/*/*/; do
    if [ -L "${d%/}" ]; then continue; fi          # los shims son symlinks — no son proyectos
    [ -f "${d}package.json" ] || continue
    rel="${d#"$REPOS_DIR"/}"; rel="${rel%/}"
    cands="${cands}${rel}
"
  done
  cands="$(printf '%s' "$cands" | sort -u | grep -v '^$' || true)"
  n="$(printf '%s\n' "$cands" | grep -c . || true)"
  if [ "$n" = 0 ]; then echo "❌ no hay proyectos frontend en repos/ (ningún package.json de 1er/2do nivel)"; exit 2; fi
  if [ "$n" -gt 1 ]; then
    echo "❌ varios proyectos frontend — pasá REPO=<uno>:"; printf '%s\n' "$cands" | sed 's/^/   - /'; exit 2
  fi
  REPO="$cands"
  echo "==> REPO autodescubierto: $REPO"
fi

# ── resolver el directorio del repo (worktree si hay TASK, si no el canónico) ──
if [ -n "$TASK" ]; then
  DIR="$WT_DIR/$TASK/$REPO"
  [ -d "$DIR" ] || { echo "❌ no existe el worktree $DIR — creá con 'make wt TASK=$TASK REPOS=$REPO'"; exit 1; }
else
  DIR="$REPOS_DIR/$REPO"
  [ -d "$DIR" ] || { echo "❌ no existe $DIR — revisá manifest.yaml y cloná los repos"; exit 1; }
fi
[ -f "$DIR/package.json" ] || { echo "❌ $DIR/package.json no existe — este repo no es un proyecto node/bun"; exit 1; }

# ── detectar package manager por lockfile en DIR ──
if   [ -f "$DIR/bun.lock" ] || [ -f "$DIR/bun.lockb" ]; then PM=bun
elif [ -f "$DIR/pnpm-lock.yaml" ];                       then PM=pnpm
elif [ -f "$DIR/yarn.lock" ];                            then PM=yarn
else                                                          PM=npm   # package-lock.json o sin lockfile
fi

# corepack (pnpm/yarn) con estado 100% LOCAL al workspace: nada en $HOME.
# COREPACK_INTEGRITY_KEYS=0 sortea el bug de firmas de pnpm en el corepack embebido en node
# (keyids desactualizados); la descarga sigue por TLS del registry oficial.
export COREPACK_HOME="$WS/.cache/corepack"
export COREPACK_INTEGRITY_KEYS=0

case "$PM" in
  bun)
    command -v bun >/dev/null 2>&1 || { echo "❌ el repo usa bun pero no está en el PATH — corre 'bash scripts/bootstrap.sh'"; exit 1; }
    RUN=(bun); VER="$(bun --version 2>/dev/null)" ;;
  npm)
    RUN=(npm); VER="npm $(npm --version 2>/dev/null)" ;;
  pnpm)
    command -v corepack >/dev/null 2>&1 || { echo "❌ el repo usa pnpm pero falta corepack (viene con node ≥16.9) — actualizá node"; exit 1; }
    RUN=(corepack pnpm); VER="pnpm $(corepack pnpm --version 2>/dev/null | tail -1)" ;;
  yarn)
    command -v corepack >/dev/null 2>&1 || { echo "❌ el repo usa yarn pero falta corepack (viene con node ≥16.9) — actualizá node"; exit 1; }
    RUN=(corepack yarn); VER="yarn $(corepack yarn --version 2>/dev/null | tail -1)" ;;
esac

echo "==> Frontend ($PM): $REPO${TASK:+  (worktree: $TASK)}"
echo "    dir:  $DIR"
echo "    pm:   $VER  ·  node: $(node --version)"
echo "    cmd:  $PM $CMD"
cd "$DIR"
# shellcheck disable=SC2086  # $CMD es un string de comando del usuario: el word-splitting es intencional
exec "${RUN[@]}" $CMD
