#!/usr/bin/env bash
# gowork.sh — genera un go.work para el LOOP INTERNO NATIVO de Go (segundos, cache
# incremental), sin depender del contenedor. Universal: descubre módulos en runtime
# desde el filesystem; nada de nombres de repos/módulos hardcodeados.
#
# Uso:
#   bash scripts/gowork.sh             genera go.work en la RAÍZ (cubre los módulos de repos/)
#   bash scripts/gowork.sh <task-id>   genera worktrees/<task-id>/go.work para esa tarea,
#                                       con FALLBACK al canónico: unión por module-path de
#                                       (módulos del worktree) ∪ (módulos de repos/), gana
#                                       el worktree si el module-path está en ambos. Así una
#                                       tarea que sólo tocó un servicio compila contra el
#                                       shared canónico.
#
# ── Resultado del experimento de replaces (repos reales de un harness) ──
# Los go.mod de los servicios suelen traer `replace <mod> => ../../pkg` (y proto) pensados
# para un layout monorepo que NO existe en el harness (repos/pkg, repos/proto/gen/go no
# existen; el shared vive en otro repo/subdir). Se probó empíricamente:
#   A) go.work sólo con `use`  → FALLA: "conflicting replacements for <proto>" — en modo
#      workspace Go SÍ aplica los replace de los go.mod miembros y chocan (el replace roto
#      del servicio resuelve a una ruta distinta que el replace válido del shared).
#   B) go.work con `replace <mod> => <dir>` (sin versión) → FALLA: "workspace module <mod>
#      is replaced at all versions ... specify the version".
#   C) go.work con `replace <mod> vX => <dir>` (VERSIONADO, X = la versión con que lo pide
#      el require) → OK, compila nativo. El replace del go.work sobreescribe a los de los
#      miembros y deshace el conflicto.
# Conclusión: `use` NO alcanza; hay que EMITIR replaces versionados en el go.work por cada
# módulo del workspace cuyo replace relativo en algún miembro apunta a una ruta inexistente.
# Todo keyed por module-path leído de los go.mod — nunca por nombre de repo.
#
# Portable: sin arrays asociativos ni mapfile (corre en el bash 3.2 de macOS y en Linux).
# Toolchain: el go.work es agnóstico de qué `go` lo lea (gopls/IDE del humano incluidos).
# Para VERIFICAR usá el `go` del PATH que garantiza scripts/bootstrap.sh. Es derivable y
# por-máquina: va gitignoreado.
set -euo pipefail

WS="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPOS_DIR="$WS/repos"
WT_DIR="$WS/worktrees"

task="${1:-}"

# ── descubrimiento: go.mod bajo un root, podando ruido y (opcional) una ruta extra ──
discover() { # $1=root  [$2=ruta absoluta a podar]
  local root="$1" extra="${2:-}"
  [ -d "$root" ] || return 0
  if [ -n "$extra" ]; then
    find "$root" \( -name .git -o -name vendor -o -name node_modules \
                    -o -name .cache -o -path "$extra" \) -prune -o -name go.mod -print
  else
    find "$root" \( -name .git -o -name vendor -o -name node_modules \
                    -o -name .cache \) -prune -o -name go.mod -print
  fi
}

# ── path de $1 relativo a $2, con prefijo ./ (perl/File::Spec: portable, no exige existencia) ──
reldir() {
  local p
  p="$(perl -MFile::Spec -e 'print File::Spec->abs2rel($ARGV[0],$ARGV[1])' "$1" "$2")"
  case "$p" in .|./*|../*) printf '%s' "$p" ;; *) printf './%s' "$p" ;; esac
}

# module-path (1ra línea `module `) y directiva `go` de un go.mod
mod_of() { awk '$1=="module"{print $2; exit}' "$1"; }
gov_of() { awk '$1=="go"{print $2; exit}' "$1"; }

# imprime "OLD<TAB>TARGET" por cada replace (single-line y en bloque) de un go.mod
replaces_of() {
  awk '
    function emit(s,   n,a,i,arrow) {
      n=split(s,a," "); arrow=0
      for(i=1;i<=n;i++) if(a[i]=="=>"){arrow=i; break}
      if(arrow && arrow<n) print a[1]"\t"a[arrow+1]
    }
    { line=$0; sub(/\/\/.*/,"",line) }
    line ~ /^[ \t]*replace[ \t]*\(/ { inblk=1; next }
    inblk && line ~ /^[ \t]*\)/     { inblk=0; next }
    inblk { gsub(/^[ \t]+/,"",line); if(line!="") emit(line); next }
    line ~ /^[ \t]*replace[ \t]+/   { sub(/^[ \t]*replace[ \t]+/,"",line); emit(line) }
  ' "$1"
}

# versión con que un go.mod pide el module-path $2 (para el replace versionado); default v0.0.0
reqver_of() {
  awk -v m="$2" '{for(i=1;i<NF;i++) if($i==m && $(i+1) ~ /^v[0-9]/){print $(i+1); exit}}' "$1" \
    | { read -r v || true; printf '%s' "${v:-v0.0.0}"; }
}

# ── selección de módulos: registros "modpath<TAB>dir<TAB>gomod"; el ÚLTIMO gana por module-path.
#    Para el fallback del worktree: cargamos repos/ primero y el worktree después (worktree gana). ──
records=""
collect() { # lee paths de go.mod por stdin y agrega registros
  local gomod mp dir
  while IFS= read -r gomod; do
    [ -n "$gomod" ] || continue
    mp="$(mod_of "$gomod")"; [ -n "$mp" ] || continue
    dir="$(cd "$(dirname "$gomod")" && pwd)"
    records="${records}${mp}	${dir}	${gomod}
"
  done
}

if [ -z "$task" ]; then
  workfile="$WS/go.work"; workdir="$WS"
  collect < <(discover "$WS" "$WT_DIR")               # raíz: poda worktrees
else
  # task-id acotado (mismo criterio que worktree-task.sh) para no cruzar dirs raros
  case "$task" in
    [A-Za-z0-9][A-Za-z0-9._-]*) ;;
    *) echo "❌ task-id inválido '$task'"; exit 1 ;;
  esac
  wtroot="$WT_DIR/$task"
  [ -d "$wtroot" ] || { echo "❌ worktree de la tarea '$task' no existe ($wtroot)"; exit 1; }
  workfile="$wtroot/go.work"; workdir="$wtroot"
  collect < <(discover "$REPOS_DIR")                  # canónico primero
  collect < <(discover "$wtroot")                     # worktree gana por module-path
fi

# ── ganadores: último registro por module-path, ordenado por module-path ──
winners="$(printf '%s' "$records" | awk -F'\t' 'NF>=3{last[$1]=$0} END{for(k in last) print last[k]}' | sort)"

# ── no-op limpio para clientes sin Go ──
if [ -z "$winners" ]; then
  echo "(sin módulos Go — go.work no aplica)"
  exit 0
fi

# dir ganador de un module-path
win_dir() { printf '%s\n' "$winners" | awk -F'\t' -v m="$1" '$1==m{print $2; exit}'; }

# ── go X.Y.Z: la mayor directiva `go` entre los módulos ──
maxgo=""
while IFS='	' read -r mp dir gomod; do
  [ -n "$gomod" ] || continue
  gv="$(gov_of "$gomod")"; [ -n "$gv" ] || continue
  if [ -z "$maxgo" ]; then maxgo="$gv"
  else maxgo="$(printf '%s\n%s\n' "$maxgo" "$gv" | sort -V | tail -1)"; fi
done <<EOF
$winners
EOF
[ -n "$maxgo" ] || maxgo="1.21"

# ── replaces rotos: por cada replace relativo de un miembro cuyo target NO existe y cuyo
#    module-path es del workspace → replace versionado hacia el dir ganador. "old<TAB>ver". ──
needs=""
while IFS='	' read -r mp dir gomod; do
  [ -n "$gomod" ] || continue
  while IFS='	' read -r old tgt; do
    [ -n "$old" ] || continue
    case "$tgt" in ./*|../*) ;; *) continue ;; esac          # sólo targets relativos
    resolved="$(perl -MFile::Spec -e 'print File::Spec->rel2abs($ARGV[0],$ARGV[1])' "$tgt" "$dir")"
    [ -e "$resolved" ] && continue                           # replace sano → nada que hacer
    [ -n "$(win_dir "$old")" ] || continue                   # el reemplazado no es del workspace
    case "
$needs" in *"
$old	"*) continue ;; esac                                   # ya registrado (dedupe por module-path)
    needs="${needs}${old}	$(reqver_of "$gomod" "$old")
"
  done < <(replaces_of "$gomod")
done <<EOF
$winners
EOF

# ── composición del go.work ──
nmods="$(printf '%s\n' "$winners" | grep -c . || true)"
nrepl=0
content="go ${maxgo}"$'\n\n'"use ("$'\n'
while IFS='	' read -r mp dir gomod; do
  [ -n "$dir" ] || continue
  content="${content}	$(reldir "$dir" "$workdir")
"
done <<EOF
$winners
EOF
content="${content})"$'\n'
if [ -n "$(printf '%s' "$needs" | tr -d '[:space:]')" ]; then
  content="${content}"$'\n'
  while IFS='	' read -r old ver; do
    [ -n "$old" ] || continue
    content="${content}replace ${old} ${ver} => $(reldir "$(win_dir "$old")" "$workdir")
"
    nrepl=$((nrepl+1))
  done < <(printf '%s' "$needs" | sort)
fi

# ── escritura atómica e idempotente ──
tmp="$(mktemp)"; printf '%s' "$content" > "$tmp"
if [ -f "$workfile" ] && cmp -s "$tmp" "$workfile"; then rm -f "$tmp"; else mv "$tmp" "$workfile"; fi

echo "✓ go.work (${nmods} módulos, ${nrepl} replaces) → $workfile"
