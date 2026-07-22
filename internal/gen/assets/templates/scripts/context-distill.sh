#!/usr/bin/env bash
# context-distill.sh: el patrón MinionS como script. Un modelo BARATO (el tier
# `reader` de models.yaml) LEE un contexto largo y lo destila en un "context
# pack" citado; después el agente CARO razona sobre el pack, no sobre el
# volcado crudo (arXiv:2502.15964: el modelo caro gasta sus tokens en JUICIO,
# no en LECTURA). $0 tokens cuando el pack está fresco (stamp por hash de las
# entradas). OPT-IN: nada lo llama salvo que context_distill=true (docs/harness/minions-context-distill.md).
#
# Uso: context-distill.sh <task-id> <slug> "<pregunta>" <archivo|glob>...
#   Escribe tasks/<task-id>/context/<slug>.md (el pack) y lo imprime en stdout.
#
# El pack SIEMPRE cita file:line: el agente caro puede tirar de la fuente
# cruda si el destilado no le basta (nunca razona a ciegas). Esa es la red que
# hace segura la compresión.
#
# Portabilidad: bash 3.2, BSD userland. Fail-open: si no hay reader/claude o
# la destilación falla, imprime los archivos crudos y sale 0 (el agente lee
# como antes; jamás bloquea el pipeline).
set -u

TASK="${1:?uso: context-distill.sh <task-id> <slug> <pregunta> <archivos...>}"
SLUG="${2:?uso: context-distill.sh <task-id> <slug> <pregunta> <archivos...>}"
QUESTION="${3:?falta la pregunta}"
shift 3
[ $# -gt 0 ] || { echo "❌ indica al menos un archivo/glob de contexto"; exit 1; }

ok_id() { case "$1" in [A-Za-z0-9][A-Za-z0-9._-]*) return 0 ;; *) return 1 ;; esac; }
ok_id "$TASK" || { echo "❌ task-id inválido: '$TASK'"; exit 1; }
ok_id "$SLUG" || { echo "❌ slug inválido: '$SLUG'"; exit 1; }

WS="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$WS/tasks/$TASK/context"
OUT="$OUT_DIR/$SLUG.md"
mkdir -p "$OUT_DIR"

# Reúne las entradas reales (expande globs, salta lo que no existe).
files=""
for pat in "$@"; do
  for f in $pat; do
    [ -f "$f" ] && files="$files $f"
  done
done
[ -n "$files" ] || { echo "⚠️  ningún archivo de contexto existe; nada que destilar" >&2; exit 0; }

# Stamp: hash de (pregunta + rutas + mtimes). Fresco ⇒ $0, imprime el pack.
stamp_now="$(printf '%s\n' "$QUESTION" $files | cksum | cut -d' ' -f1)$(
  for f in $files; do stat -c %Y "$f" 2>/dev/null || stat -f %m "$f" 2>/dev/null; done | cksum | cut -d' ' -f1)"
STAMP="$OUT.stamp"
if [ -f "$OUT" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "$stamp_now" ]; then
  cat "$OUT"; exit 0
fi

# Resuelve el modelo reader (aliases → id, misma fuente que todo el harness).
reader_id=""
[ -x "$WS/scripts/stamp-models.sh" ] && reader_id="$(bash "$WS/scripts/stamp-models.sh" resolve reader 2>/dev/null || true)"

raw_dump() {  # fallback: el volcado crudo con separadores (lo que se leía antes)
  for f in $files; do
    echo "### $f"
    cat "$f"
    echo
  done
}

if [ -z "$reader_id" ] || ! command -v claude >/dev/null 2>&1; then
  echo "⚠️  sin reader/claude: devuelvo el contexto crudo (fail-open)" >&2
  raw_dump; exit 0
fi

PROMPT="Eres el destilador de contexto (patrón MinionS). NO opines ni decidas:
EXTRAE del material lo que responde a la pregunta, con máxima densidad y
CITANDO cada dato como \`archivo:línea\`. El que lea esto es un modelo caro
que razonará sobre tu destilado; si omites algo relevante, lo pierde. Formato:
viñetas cortas, cada una con su cita. Al final, una sección 'INCERTIDUMBRE:'
con lo que el material NO deja claro (para que el modelo caro tire de la fuente
si hace falta).

PREGUNTA: $QUESTION

MATERIAL:
$(raw_dump)"

pack="$(printf '%s' "$PROMPT" | claude -p --model "$reader_id" 2>/dev/null || true)"
if [ -z "$pack" ]; then
  echo "⚠️  destilación vacía: devuelvo el contexto crudo (fail-open)" >&2
  raw_dump; exit 0
fi

{
  echo "<!-- context pack @ $SLUG · reader=$reader_id · NO editar (regenera context-distill.sh) -->"
  echo "# Contexto destilado: $QUESTION"
  echo
  printf '%s\n' "$pack"
  echo
  echo "---"
  echo "_Fuentes crudas (tira de ellas si el destilado no basta): $(echo $files | tr ' ' '\n' | sed 's/^/- /' | tr '\n' ' ')_"
} > "$OUT"
printf '%s' "$stamp_now" > "$STAMP"
cat "$OUT"
