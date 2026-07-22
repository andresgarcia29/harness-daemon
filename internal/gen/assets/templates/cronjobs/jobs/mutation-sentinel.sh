# mutation-sentinel — semanal, time-boxed. ¿Los tests PROTEGEN o son
# teatro? Corre mutation testing SOLO sobre lo cambiado en la semana;
# los mutantes supervivientes en rutas críticas despiertan al agente
# para escribir el test que los mata (patrón Meta ACH).
JOB_NAME=mutation-sentinel
JOB_TIER=expensive
JOB_MAX_TURNS=80
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(go *),Bash(npm *),Bash(npx *),Bash(uv *),Bash(mutmut *),Bash(go-mutesting *),Edit,Write"

detect() {
  local any_tool=0
  command -v go-mutesting >/dev/null && any_tool=1
  command -v mutmut >/dev/null && any_tool=1
  command -v npx >/dev/null && any_tool=1   # stryker (TS/JS) vía npx
  [ "$any_tool" -eq 1 ] || return 3
  : > "$FINDINGS"
  for r in repos/*/; do
    [ -d "$r/.git" ] || continue
    local name; name="$(basename "$r")"
    local changed; changed="$(git -C "$r" diff --name-only "@{7 days ago}" 2>/dev/null | grep -vE '_test\.|test_' || true)"
    [ -n "$changed" ] || continue
    if [ -f "$r/go.mod" ] && command -v go-mutesting >/dev/null; then
      local pkgs; pkgs="$(echo "$changed" | grep '\.go$' | xargs -I{} dirname {} 2>/dev/null | sort -u | head -5)"
      [ -n "$pkgs" ] && (cd "$r" && echo "$pkgs" | while read -r p; do
        timeout 600 go-mutesting "./$p/" 2>/dev/null | grep -E "^FAIL" | sed "s|^|$name: |"
      done) >> "$FINDINGS" || true
    fi
    if [ -f "$r/pyproject.toml" ] && command -v mutmut >/dev/null; then
      (cd "$r" && timeout 900 mutmut run --paths-to-mutate "$(echo "$changed" | grep '\.py$' | paste -sd, -)" >/dev/null 2>&1
       mutmut results 2>/dev/null | grep -i surviv | sed "s|^|$name: |") >> "$FINDINGS" || true
    fi
    # stryker (TS/JS): solo si el repo YA tiene su config — sin config no se inventa
    if [ -f "$r/package.json" ] && command -v npx >/dev/null \
       && { [ -f "$r/stryker.conf.json" ] || [ -f "$r/stryker.config.json" ] || [ -f "$r/stryker.config.mjs" ]; }; then
      local ts_changed; ts_changed="$(echo "$changed" | grep -E '\.(ts|tsx|js|jsx)$' | head -5 | paste -sd, -)"
      [ -n "$ts_changed" ] && (cd "$r" \
        && timeout 900 npx stryker run --mutate "$ts_changed" --reporters json >/dev/null 2>&1
        jq -r '.files | to_entries[] | .key as $f | .value.mutants[]? | select(.status=="Survived") | "\($f): \(.mutatorName) línea \(.location.start.line)"' \
          reports/mutation/mutation.json 2>/dev/null | head -20 | sed "s|^|$name: |") >> "$FINDINGS" || true
    fi
  done
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el mutation-sentinel. Los hallazgos son MUTANTES
SUPERVIVIENTES: código que puede romperse sin que ningún test lo note.
Por cada uno en rutas críticas (auth, pagos, contratos, tenancy):
(1) entiende qué invariante quedó sin proteger; (2) escribe el test
que MATA al mutante (no un test que cubre la línea: uno que falla con
la mutación); (3) verifica re-corriendo la herramienta de mutación
sobre ese archivo; (4) un PR por repo titulado "test: mata N mutantes
en <área>". Si el mutante revela un bug real (el comportamiento mutado
parece el correcto), abre issue P1 en vez de test.'
