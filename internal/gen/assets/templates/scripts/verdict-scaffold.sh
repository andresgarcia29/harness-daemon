#!/usr/bin/env bash
# verdict-scaffold.sh: esqueleto DETERMINISTA del veredicto. El reviewer no
# puede saber (ni debe adivinar) los campos mecánicos: commit, evidencia e
# implementadores salen de fuentes verificables; él solo pone JUICIO.
#
# Fuentes (espejan a evidence.py verify y harness-policy validate-ship):
#   commit                 = HEAD del worktree de la tarea
#   evidence[]             = ids de tasks/<t>/evidence/EV-*.json con
#                            repo+commit+commit_after correctos y exit_code 0
#   implementation_agents  = runners únicos de esa evidencia, excluyendo al
#                            reviewer y a "qa" (beads no guarda identidad;
#                            el runner del EV es la fuente honesta)
# Placeholders INESCAPABLES para los gates: verdict:"PENDING_REVIEWER",
# qa:"pending", y requirements_uncovered:-1 (JAMÁS null/0: el check hace
# (// 0)==0 y null pasaría).
#
# Uso: verdict-scaffold.sh [--force] [--allow-empty] <task-id> <repo> [reviewer]
#   --allow-empty  para el /review paralelo (la evidencia de QA llega después)
#   --force        re-scaffold (imprime commit/verdict previos; el juicio se pierde)
# Portabilidad: bash 3.2, BSD userland, jq.
set -euo pipefail

FORCE=0
ALLOW_EMPTY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --force) FORCE=1; shift ;;
    --allow-empty) ALLOW_EMPTY=1; shift ;;
    --*) echo "❌ flag desconocido: $1"; exit 1 ;;
    *) break ;;
  esac
done
TASK="${1:?uso: verdict-scaffold.sh [--force] [--allow-empty] <task-id> <repo> [reviewer]}"
REPO="${2:?uso: verdict-scaffold.sh [--force] [--allow-empty] <task-id> <repo> [reviewer]}"
REVIEWER="${3:-reviewer}"

ok_id() { case "$1" in [A-Za-z0-9][A-Za-z0-9._-]*) return 0 ;; *) return 1 ;; esac; }
ok_id "$TASK" || { echo "❌ task-id inválido: '$TASK'"; exit 1; }
ok_id "$REPO" || { echo "❌ repo inválido: '$REPO'"; exit 1; }
ok_id "$REVIEWER" || { echo "❌ reviewer inválido: '$REVIEWER'"; exit 1; }
command -v jq >/dev/null || { echo "❌ jq requerido"; exit 1; }

WS="$(cd "$(dirname "$0")/.." && pwd)"
WT="$WS/worktrees/$TASK/$REPO"
[ -d "$WS/tasks/$TASK" ] || { echo "❌ no existe tasks/$TASK (¿typo?)"; exit 1; }
HEAD="$(git -C "$WT" rev-parse HEAD 2>/dev/null)" \
  || { echo "❌ no existe el worktree $WT"; echo "   ↳ remediación: scripts/worktree-task.sh $TASK $REPO"; exit 2; }

OUT="$WS/tasks/$TASK/verdict-$REPO.json"
if [ -f "$OUT" ]; then
  prev_c="$(jq -r '.commit // "?"' "$OUT" 2>/dev/null | cut -c1-12)"
  prev_v="$(jq -r '.verdict // "?"' "$OUT" 2>/dev/null)"
  if [ "$FORCE" -ne 1 ]; then
    echo "❌ ya existe $OUT (commit $prev_c, verdict $prev_v)"
    echo "   ↳ para el re-review incremental usa ese commit como base; --force re-scaffoldea (el juicio previo se pierde)"
    exit 3
  fi
  echo "⚠️  sobreescribo veredicto previo: commit=$prev_c verdict=$prev_v"
fi

# ── Selección de evidencia: mismos criterios estructurales que verify_one ──
rows=""
stale=0
for f in "$WS/tasks/$TASK/evidence"/EV-*.json; do
  [ -e "$f" ] || continue
  line="$(jq -r --arg t "$TASK" --arg r "$REPO" --arg c "$HEAD" '
    select(type == "object" and .schema == 1 and .task_id == $t and .repo == $r)
    | if .commit == $c and .commit_after == $c and .exit_code == 0
      then [.id, (.runner // ""), (.kind // "")] | join("|")
      else "STALE" end
  ' "$f" 2>/dev/null || true)"
  case "$line" in
    "") ;;
    STALE) stale=$((stale+1)) ;;
    *)
      id="${line%%|*}"
      [ "$id" = "$(basename "$f" .json)" ] || continue   # id falsificado: fuera
      rows="$rows$line
"
      ;;
  esac
done
rows="$(printf '%s' "$rows" | sort)"   # orden estable ⇒ scaffold idempotente a bytes

if [ -z "$rows" ] && [ "$ALLOW_EMPTY" -ne 1 ]; then
  if [ "$stale" -gt 0 ]; then
    echo "❌ hay $stale evidencia(s) de $REPO pero de OTRO commit (HEAD actual: ${HEAD:0:12})"
    echo "   ↳ el implementer movió HEAD después de generarlas: re-corre la evidencia sobre el HEAD actual:"
  else
    echo "❌ cero evidencias de $REPO@${HEAD:0:12} en tasks/$TASK/evidence/"
    echo "   ↳ primero genera evidencia real:"
  fi
  echo "     python3 scripts/evidence.py run --task-dir tasks/$TASK --repo $REPO \\"
  echo "       --runner <implementer> --kind test --cwd worktrees/$TASK/$REPO -- <comando de test>"
  exit 3
fi

tmp="$(mktemp "$WS/tasks/$TASK/.verdict-$REPO.XXXXXX")"
printf '%s\n' "$rows" | jq -RnS --arg task "$TASK" --arg repo "$REPO" \
    --arg commit "$HEAD" --arg reviewer "$REVIEWER" '
  [inputs | select(length > 0) | split("|")] as $rows
  | { schema: 1, task_id: $task, repo: $repo, commit: $commit,
      reviewer: $reviewer,
      implementation_agents:
        ($rows | map(.[1]) | map(select(. != "" and . != $reviewer and . != "qa")) | unique),
      evidence: ($rows | map(.[0])),
      verdict: "PENDING_REVIEWER",
      qa: "pending",
      blocking: [], non_blocking: [],
      docs_updated: false,
      compliance: [],
      requirements_uncovered: -1,
      snapshots_updated_justified: false }' > "$tmp"

# Detección temprana de violación de roles: si TODA la evidencia la corrió
# qa o el propio reviewer, ship morirá en POLICY-ROLE-002/003. Mejor aquí.
if [ "$(jq '.implementation_agents | length' "$tmp")" -eq 0 ] && [ "$ALLOW_EMPTY" -ne 1 ]; then
  runners="$(printf '%s\n' "$rows" | cut -d'|' -f2 | sort -u | tr '\n' ' ')"
  rm -f "$tmp"
  echo "❌ ningún runner de IMPLEMENTACIÓN en la evidencia (runners: ${runners:-ninguno})"
  echo "   ↳ la evidencia del implementer corre con --runner <su identidad>;"
  echo "     el reviewer y qa no pueden ser los implementadores (política de roles)"
  exit 3
fi
jq -e '.evidence | map(select(startswith("EV-TEST-"))) | length > 0' "$tmp" >/dev/null 2>&1 \
  || echo "⚠️  sin evidencia kind=test todavía: evidence.py verify --require-kind test fallará en ship si nadie la aporta"

mv "$tmp" "$OUT"
echo "✅ scaffold: tasks/$TASK/verdict-$REPO.json ($(jq '.evidence|length' "$OUT") evidencias, agents=$(jq -c '.implementation_agents' "$OUT"), commit ${HEAD:0:12})"
echo "→ el reviewer reemplaza SOLO el juicio: verdict, blocking, non_blocking,"
echo "  compliance, requirements_uncovered, docs_updated, snapshots_updated_justified."
echo "  El campo qa lo fusiona /review desde qa-$REPO.json."
