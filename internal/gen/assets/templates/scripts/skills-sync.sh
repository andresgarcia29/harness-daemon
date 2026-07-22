#!/usr/bin/env bash
# skills-sync.sh: instala y actualiza las skills COMPARTIDAS desde tus repos
# (skills.yaml), sin tocar jamás las upstream del plugin ni las locales.
#
# LAS TRES CAPAS (la procedencia es verificable, no una convención de fe):
#   upstream    las trae el plugin; las gobierna el manifest del generador
#   compartida  viene de un repo tuyo via skills.yaml; lleva marca .managed
#               (repo + ref + sha exactos: procedencia auditable)
#   local       sin marca alguna: humano o skill-miner; NADIE la pisa
#
# Reglas:
#   · una skill local con el mismo nombre que una compartida GANA: el sync
#     reporta el choque y no la toca (renombra una de las dos)
#   · una compartida que desaparece de skills.yaml se DESINSTALA (la marca
#     .managed hace seguro el prune: solo se borra lo que este script puso)
#   · --check: exit 1 si el sync cambiaría algo (lo usa el doctor)
# Portabilidad: bash 3.2, sin GNU-ismos. Requiere git.
set -u

WS="$(cd "$(dirname "$0")/.." && pwd)"
CONF="$WS/skills.yaml"
DEST="$WS/.claude/skills"
SRC_CACHE="$WS/.cache/skills-src"
CHECK=0
[ "${1:-}" = "--check" ] && CHECK=1

[ -f "$CONF" ] || { echo "sin skills.yaml: nada que sincronizar (capa compartida vacía)"; exit 0; }
mkdir -p "$DEST" "$SRC_CACHE"

slug() { printf '%s' "$1" | sed 's|.*[:/]\([^/]*\)/\([^/]*\)$|\1-\2|; s|\.git$||; s|[^A-Za-z0-9._-]|-|g'; }

fails=0
changes=0
WANTED="$SRC_CACHE/.wanted.$$"
: > "$WANTED"

# formato ESTRICTO (como models.yaml): bajo "sources:", una línea por fuente:
#   - <repo-git> <skill|all> [ref]
while IFS= read -r line; do
  repo="$(printf '%s' "$line" | awk '{print $2}')"
  want="$(printf '%s' "$line" | awk '{print $3}')"
  ref="$(printf '%s' "$line" | awk '{print $4}')"
  [ -n "$repo" ] && [ -n "$want" ] || continue

  s="$(slug "$repo")"
  src="$SRC_CACHE/$s"
  if [ -d "$src/.git" ]; then
    git -C "$src" fetch -q origin 2>/dev/null || { echo "✗ $repo: fetch falló (¿red/acceso?)"; fails=$((fails+1)); continue; }
  else
    git clone -q "$repo" "$src" 2>/dev/null || { echo "✗ $repo: clone falló (¿existe? ¿acceso?)"; fails=$((fails+1)); continue; }
  fi
  [ -n "$ref" ] || ref="$(git -C "$src" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|origin/||')"
  [ -n "$ref" ] || ref="main"
  git -C "$src" checkout -q "origin/$ref" 2>/dev/null || git -C "$src" checkout -q "$ref" 2>/dev/null \
    || { echo "✗ $repo: ref '$ref' no existe"; fails=$((fails+1)); continue; }
  sha="$(git -C "$src" rev-parse --short HEAD)"

  if [ "$want" = "all" ]; then
    list="$(cd "$src" && ls -d */ 2>/dev/null | sed 's|/||')"
  else
    list="$want"
  fi
  for sk in $list; do
    [ -f "$src/$sk/SKILL.md" ] || { [ "$want" = "all" ] || { echo "✗ $repo: no trae la skill '$sk' (falta $sk/SKILL.md)"; fails=$((fails+1)); }; continue; }
    echo "$sk" >> "$WANTED"
    tgt="$DEST/$sk"
    if [ -d "$tgt" ] && [ ! -f "$tgt/.managed" ]; then
      echo "✗ choque: .claude/skills/$sk existe y es LOCAL (sin .managed); la local gana. Renombra una de las dos."
      fails=$((fails+1)); continue
    fi
    cur=""
    [ -f "$tgt/.managed" ] && cur="$(head -1 "$tgt/.managed" 2>/dev/null)"
    stamp="$repo@$ref#$sha"
    if [ "$cur" = "$stamp" ]; then
      echo "✓ $sk: al día ($sha, de $(basename "$repo" .git))"
      continue
    fi
    changes=$((changes+1))
    if [ "$CHECK" -eq 1 ]; then
      echo "△ $sk: desactualizada (instalada: ${cur:-nada}; fuente: $stamp)"
      continue
    fi
    rm -rf "$tgt"
    cp -R "$src/$sk" "$tgt"
    printf '%s\nmanaged-by: skills-sync (NO editar aquí: edita en el repo fuente)\n' "$stamp" > "$tgt/.managed"
    echo "⇊ $sk: instalada @ $sha (de $(basename "$repo" .git))"
  done
done <<EOF
$(awk '/^sources:/{f=1;next} /^[^ #]/{f=0} f && /^  - /' "$CONF")
EOF

# prune: compartidas (.managed) que ya nadie declara
for d in "$DEST"/*/; do
  [ -f "$d/.managed" ] || continue
  name="$(basename "$d")"
  if ! grep -qx "$name" "$WANTED" 2>/dev/null; then
    changes=$((changes+1))
    if [ "$CHECK" -eq 1 ]; then
      echo "△ $name: ya no está en skills.yaml (se desinstalaría)"
    else
      rm -rf "$d"
      echo "⇈ $name: desinstalada (salió de skills.yaml)"
    fi
  fi
done
rm -f "$WANTED"

if [ "$CHECK" -eq 1 ] && [ "$changes" -gt 0 ]; then
  echo "── drift: $changes cambio(s) pendientes; remediación: make skills"
  exit 1
fi
[ "$fails" -gt 0 ] && { echo "── $fails fuente(s)/skill(s) con problema; detalle arriba"; exit 1; }
[ "$CHECK" -eq 1 ] && echo "── capa compartida en sync"
exit 0
