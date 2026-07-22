# doc-gardener — el jardinero nocturno: links rotos, símbolos citados
# que ya no existen, diagramas D2 drifteados, specs sin verificar.
JOB_NAME=doc-gardener
JOB_TIER=cheap
JOB_MAX_TURNS=50
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(d2 *),Bash(lychee *),Edit,Write"

detect() {
  : > "$FINDINGS"
  # 0) ley de estilo (constitución 5b): el guion largo "—" delata texto de
  #    IA y está prohibido en docs/specs. El jardinero lo reescribe.
  grep -rn "—" docs/ specs/ README.md 2>/dev/null | head -20 \
    | sed 's/^/em-dash-prohibido: /' >> "$FINDINGS" || true
  # 1) links rotos (lychee si existe; fallback: paths internos)
  if command -v lychee >/dev/null; then
    lychee --offline --no-progress docs/ CLAUDE.md 2>/dev/null | grep -E "✗|ERROR" | sed 's/^/link-roto: /' >> "$FINDINGS" || true
  else
    grep -rhoE '\((docs|scripts|specs|\.claude)/[A-Za-z0-9._/\-]+\)' docs/ CLAUDE.md 2>/dev/null \
      | tr -d '()' | sort -u | while read -r p; do
        [ -e "$p" ] || echo "link-roto: $p" >> "$FINDINGS"
      done
  fi
  # 2) símbolos/rutas citados en docs que ya no existen en repos/
  grep -rhoE '`[a-zA-Z_][a-zA-Z0-9_.]{5,}\(\)`|`[a-z0-9/_-]+\.(go|py|ts|dart|proto)`' docs/ 2>/dev/null \
    | tr -d '`' | sort -u | head -100 | while read -r sym; do
      case "$sym" in
        *.*) find repos -name "$(basename "$sym")" -print -quit 2>/dev/null | grep -q . || echo "símbolo-perdido: $sym" >> "$FINDINGS" ;;
        *)   grep -rq "${sym%()}" repos --include="*.go" --include="*.py" --include="*.ts" -m1 2>/dev/null || echo "símbolo-perdido: $sym" >> "$FINDINGS" ;;
      esac
    done
  # 3) diagramas D2: regenerar y comparar
  if command -v d2 >/dev/null; then
    find docs -name "*.d2" 2>/dev/null | while read -r f; do
      d2 "$f" "$f.new.svg" >/dev/null 2>&1 || continue
      if [ -f "${f%.d2}.svg" ] && ! cmp -s "$f.new.svg" "${f%.d2}.svg"; then
        echo "diagrama-drift: $f" >> "$FINDINGS"
      fi
      rm -f "$f.new.svg"
    done
  fi
  # 4) specs sin verificar hace >30 días
  find specs -name "spec.md" -mtime +30 2>/dev/null | sed 's/^/spec-vieja: /' >> "$FINDINGS" || true
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el doc-gardener (jardinero nocturno). Hallazgos:
(1) link-roto → corrige la referencia o elimina la línea muerta;
(2) símbolo-perdido → verifica contra el código actual y reescribe la
sección con la verdad de HOY (cita archivo:línea real);
(3) diagrama-drift → regenera el SVG desde el .d2 y committea ambos;
(4) spec-vieja → verifica 2-3 requirements contra el código; si
siguen ciertos actualiza verified_at, si no, marca el requirement con
⚠️ DRIFT y abre issue. Todo en UN PR "docs: jardinería nocturna
$(date +%F)". No inventes contenido: lo que no puedas verificar,
márcalo, no lo redactes.'
