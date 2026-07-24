#!/usr/bin/env bash
# plan-lint.sh: el plan es EJECUTABLE o no es un plan.
#
# POR QUÉ EXISTE: la re-iteración cara del pipeline casi nunca nace en el
# código, nace en el plan. Un plan que no nombra archivos, que deja un
# criterio difuso o que dice "investigar si...", delega la decisión al
# implementer; el implementer elige; el reviewer no está de acuerdo; y ahí
# se va una ronda de 10-20 minutos. Este script comprueba de forma
# determinista (cero tokens, cero juicio) que el plan trae TODO lo que el
# implementer necesita para no adivinar.
#
# Uso: plan-lint.sh <task-id>
#   exit 0 = plan ejecutable · 2 = falta el artefacto · 3 = plan rojo
#
# Qué exige, por tarea del plan (bloque '### T<n> ...'):
#   repo · req · archivos · criterios · complexity (low|high) · deps
# Y sobre el plan completo:
#   · cero vaguedad declarada (TBD, por definir, investigar si, no está claro)
#   · cada ID de `req:` existe de verdad en delta-spec.md (trazabilidad
#     req -> tarea -> test, la misma que el reviewer usa en su matriz)
#
# Portabilidad: bash 3.2 (macOS), awk y grep BSD. Sin dependencias.
set -u

TASK="${1:-}"
[ -n "$TASK" ] || { echo "uso: plan-lint.sh <task-id>"; exit 2; }
case "$TASK" in
  [A-Za-z0-9]*) : ;;
  *) echo "❌ task-id inválido: '$TASK'"; exit 2 ;;
esac
case "$TASK" in *..*|*/*) echo "❌ task-id inválido: '$TASK'"; exit 2 ;; esac

WS="$(cd "$(dirname "$0")/.." && pwd)"
DIR="$WS/tasks/$TASK"
PLAN="$DIR/plan.md"
DELTA="$DIR/delta-spec.md"

red=0
say() { printf '   · %s\n' "$1"; }

[ -f "$PLAN" ] || {
  echo "❌ no existe $PLAN"
  echo "   ↳ remediación: el plan es el artefacto de la fase RFC (o el mini-plan"
  echo "     del carril express). Escríbelo antes de pedir implement."
  exit 2
}

echo "── plan-lint: $TASK"

# ── 1. Bloques de tarea con TODAS sus claves ──────────────────────────
# Formato (lo produce el architect; ver .claude/agents/architect.md):
#   ### T1 · <repo> · <título>
#   - repo: atlas
#   - req: GW-4, GW-5
#   - archivos: internal/ratelimit/limiter.go, internal/http/middleware.go
#   - criterios: 429 tras 100 req/min por tenant (verificable por test)
#   - complexity: low
#   - deps: ninguna
missing="$(awk '
  BEGIN { need = "repo req archivos criterios complexity deps" }
  function flush(   i, n, arr, miss) {
    if (id == "") return
    n = split(need, arr, " ")
    miss = ""
    for (i = 1; i <= n; i++)
      if (index(keys " ", " " arr[i] " ") == 0) miss = miss " " arr[i]
    if (miss != "") printf "%s: faltan claves:%s\n", id, miss
    else if (cx !~ /^(low|high)$/) printf "%s: complexity debe ser low|high (era \"%s\")\n", id, cx
    id = ""; keys = " "; cx = ""
  }
  /^###[ \t]+T[0-9]+/ {
    flush()
    id = $0; sub(/^###[ \t]+/, "", id); sub(/[ \t]*$/, "", id)
    keys = " "; cx = ""
    next
  }
  /^[ \t]*[-*][ \t]+[A-Za-z_]+[ \t]*:/ {
    if (id == "") next
    line = $0
    sub(/^[ \t]*[-*][ \t]+/, "", line)
    k = line; sub(/[ \t]*:.*$/, "", k); k = tolower(k)
    keys = keys k " "
    if (k == "complexity") {
      v = line; sub(/^[^:]*:[ \t]*/, "", v); sub(/[ \t]*$/, "", v); cx = tolower(v)
    }
    next
  }
  END { flush() }
' "$PLAN")"

tasks_n="$(grep -cE '^###[ \t]+T[0-9]+' "$PLAN" || true)"
if [ "${tasks_n:-0}" -eq 0 ]; then
  echo "❌ el plan no declara ni una tarea"
  echo "   ↳ remediación: cada tarea es un bloque '### T<n> · <repo> · <título>'"
  echo "     con sus claves repo/req/archivos/criterios/complexity/deps."
  red=1
elif [ -n "$missing" ]; then
  echo "❌ tareas del plan incompletas (el implementer tendría que adivinar):"
  printf '%s\n' "$missing" | while IFS= read -r l; do [ -n "$l" ] && say "$l"; done
  echo "   ↳ remediación: sin 'archivos' no hay paralelo demostrable, sin 'req' no"
  echo "     hay matriz de compliance, y sin 'criterios' binarios el review es opinión."
  red=1
fi

# ── 2. Vaguedad declarada: lo que el plan no decidió, lo decide el loop ──
# OJO con "todo": en español es una palabra normal ("todo el diff"), así que
# solo cuenta el marcador de código "TODO:" en mayúsculas y con dos puntos.
vague="$( { grep -niE '(^|[^A-Za-z])(tbd|por definir|a definir|pendiente de decidir|investigar si|no est(á|a) claro|ya veremos)([^A-Za-z]|$)' "$PLAN" || true
            grep -nE '(^|[^A-Za-z])(TODO|FIXME|XXX):' "$PLAN" || true; } | sort -n -u)"
if [ -n "$vague" ]; then
  echo "❌ el plan deja decisiones abiertas:"
  printf '%s\n' "$vague" | while IFS= read -r l; do [ -n "$l" ] && say "$l"; done
  echo "   ↳ remediación: resuélvelas AHORA (una probe es más barata que una ronda"
  echo "     de review) o sácalas del scope y déjalas como bead de seguimiento."
  red=1
fi

# ── 3. Trazabilidad: cada req citado existe en el delta-spec ──────────
if [ -f "$DELTA" ]; then
  if ! grep -qiE '^#+[ \t]*(ADDED|MODIFIED|REMOVED)' "$DELTA"; then
    echo "❌ delta-spec.md sin secciones ADDED/MODIFIED/REMOVED"
    echo "   ↳ remediación: el delta ES la definición formal del blast radius;"
    echo "     gate_tests_untouched y la matriz del reviewer leen esas secciones."
    red=1
  fi
  reqs="$(awk '
    /^[ \t]*[-*][ \t]+[Rr]eq[ \t]*:/ {
      line = $0; sub(/^[^:]*:[ \t]*/, "", line)
      n = split(line, arr, /[,;]/)
      for (i = 1; i <= n; i++) { r = arr[i]; gsub(/^[ \t]+|[ \t]+$/, "", r); if (r != "") print r }
    }
  ' "$PLAN" | sort -u)"
  huerfanos=""
  while IFS= read -r r; do
    [ -n "$r" ] || continue
    grep -qF -- "$r" "$DELTA" || huerfanos="$huerfanos $r"
  done <<REQEOF
$reqs
REQEOF
  if [ -n "$huerfanos" ]; then
    echo "❌ el plan cita requirements que el delta-spec no define:$huerfanos"
    echo "   ↳ remediación: o el ID está mal escrito, o ese requirement todavía no"
    echo "     existe. Un req sin entrada en el delta no lo puede cubrir ningún test."
    red=1
  fi
else
  echo "❌ no existe $DELTA"
  echo "   ↳ remediación: TODOS los carriles producen delta-spec (express con 2-6"
  echo "     líneas EARS bajo '## ADDED Requirements'). El carril recorta"
  echo "     deliberación, jamás artefactos."
  red=1
fi

if [ "$red" -eq 0 ]; then
  echo "✅ plan ejecutable: $tasks_n tarea(s), claves completas, sin decisiones abiertas"
  exit 0
fi
echo ""
echo "⛔ plan rojo. Arréglalo ANTES de lanzar implementers: cada hueco de aquí"
echo "   se paga después en rondas de review, que es el minuto más caro."
exit 3
