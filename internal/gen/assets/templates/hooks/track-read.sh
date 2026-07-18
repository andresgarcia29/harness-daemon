#!/usr/bin/env bash
# track-read.sh — el libro de a bordo de la evidencia. PostToolUse sobre
# Read/Grep/Glob/Bash: apunta QUÉ artefactos abrió realmente un agente, en
# tasks/<id>/evidence.log. ship.sh (gate_evidence) intersecta lo CITADO por la
# compliance matrix con lo LEÍDO aquí.
#
# POR QUÉ EXISTE: el reviewer escribe una matriz que dice "AUTH-3 está cubierto
# por auth_test.go". Nada comprobaba que hubiera abierto auth_test.go. La ley del
# harness es que los agentes proponen y los sistemas deterministas verifican;
# sin este registro, el verificador proponía.
#
# ── LA TAREA SE DERIVA DE LA RUTA, NO SE GUARDA ──
# La primera versión leía .harness/current-task: UN archivo global. Con dos
# sesiones abiertas (y tenemos diez) la segunda pisaba a la primera y la
# evidencia se apuntaba en la tarea EQUIVOCADA — un gate podía pasar con
# evidencia de otro trabajo. Peor: nadie escribía ese archivo, así que el hook
# salía siempre sin registrar nada y gate_evidence bloqueaba TODOS los ships.
#
# La ruta ya dice la tarea: worktrees/<task>/<repo>/... La derivamos de ahí.
# Sin estado compartido no hay estado que corromper: diez sesiones en diez
# worktrees se atribuyen solas, y no hay archivo que se pueda quedar rancio.
# Es la misma lección que el lock de ship.sh — el estado compartido es el bug.
#
# Como ui-emit.sh: OBSERVA, jamás bloquea, sale 0 SIEMPRE (fail-open). Los que
# bloquean (block-direct-push, guard-canonical) son fail-CLOSED a propósito.
set -u

exit_ok() { exit 0; }
trap exit_ok EXIT
command -v jq >/dev/null 2>&1 || exit 0

WS="${CLAUDE_PROJECT_DIR:-$PWD}"

# task_of <ruta> → el id de la tarea dueña de esa ruta, o vacío.
# Acepta rutas absolutas o relativas al workspace.
task_of() {
  case "$1" in
    */worktrees/*) printf '%s' "${1#*/worktrees/}" | cut -d/ -f1 ;;
    worktrees/*)   printf '%s' "${1#worktrees/}"   | cut -d/ -f1 ;;
    *) : ;;
  esac
}

payload="$(cat 2>/dev/null)"; [ -n "$payload" ] || exit 0
tool="$(printf '%s' "$payload" | jq -r '.tool_name // ""' 2>/dev/null)"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sid="$(printf '%s' "$payload" | jq -r '.session_id // ""' 2>/dev/null)"

# emit <kind> <ruta-o-cmd> <ruta-para-derivar-tarea>
emit() {
  local task; task="$(task_of "$3")"
  [ -n "$task" ] || return 0                       # fuera de un worktree: no es evidencia de ninguna tarea
  case "$task" in *[!A-Za-z0-9._-]*) return 0 ;; esac   # id raro → no construimos rutas con él
  local log="$WS/tasks/$task/evidence.log"
  mkdir -p "$WS/tasks/$task" 2>/dev/null || return 0
  printf '%s\t%s\t%s\t%s\n' "$ts" "$sid" "$1" "$2" >> "$log" 2>/dev/null
}

case "$tool" in
  Read|NotebookRead)
    p="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // ""' 2>/dev/null)"
    [ -n "$p" ] && emit read "${p#"$WS"/}" "$p"
    ;;
  Grep|Glob)
    p="$(printf '%s' "$payload" | jq -r '.tool_input.path // ""' 2>/dev/null)"
    [ -n "$p" ] && emit scan "${p#"$WS"/}" "$p"
    ;;
  Bash)
    # Un test que CORRIÓ es la evidencia más fuerte que hay. La tarea se deriva
    # del directorio donde corrió (cwd del tool), que también es el worktree.
    cmd="$(printf '%s' "$payload" | jq -r '.tool_input.command // ""' 2>/dev/null)"
    [ -n "$cmd" ] || exit 0
    cwd="$(printf '%s' "$payload" | jq -r '.cwd // ""' 2>/dev/null)"
    # El cwd del hook es el de la SESIÓN (la raíz del workspace), no el del
    # comando: un "cd worktrees/COR-42/atlas && go test" corre en el worktree
    # pero el cwd reportado es la raíz. La tarea viene del TEXTO del comando.
    hint="$cwd"
    case "$cmd" in *worktrees/*) hint="worktrees/${cmd#*worktrees/}" ;; esac
    case "$cmd" in
      *test*|*spec*|*pytest*|*jest*|*vitest*|*rspec*|*"go test"*|*gradle*|*mvn*|*cargo*)
        emit ran "$(printf '%s' "$cmd" | cut -c1-160)" "$hint"
        for tok in $cmd; do
          case "$tok" in
            -*|*=*) continue ;;
            *[/.]*) : ;;              # solo tokens que parecen ruta/archivo
            *) continue ;;            # ("test" de "go test" no es un artefacto)
          esac
          case "$tok" in
            *test*|*spec*|*.go|*.py|*.ts|*.js|*.rs|*.java|*.rb)
              # la ruta del archivo de test manda sobre el cwd si trae worktree
              t="$tok"; case "$t" in /*) : ;; *) t="$cwd/$tok" ;; esac
              emit ran-file "${tok#"$WS"/}" "$t" ;;
          esac
        done
        ;;
    esac
    ;;
esac
exit 0
