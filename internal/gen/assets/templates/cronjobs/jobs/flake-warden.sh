# flake-warden — flakiness: mismo commit, pass y fail. Detecta desde los
# JUnit XML que gotestsum/pytest/vitest archivan en .cache/junit/<repo>/.
# Política GitHub-interna: detectar → CUARENTENA → delegar el fix.
JOB_NAME=flake-warden
JOB_TIER=expensive
JOB_MAX_TURNS=60
JOB_TOOLS="Read,Grep,Glob,Bash(git *),Bash(gh *),Bash(go *),Bash(npm *),Bash(uv *),Bash(gotestsum *),Edit,Write"

detect() {
  local dir=".cache/junit"
  [ -d "$dir" ] || { echo "sin $dir — archiva los JUnit XML de cada run de CI ahí" >&2; return 3; }
  # tests que aparecen con failure Y sin failure en los XML de la semana
  find "$dir" -name "*.xml" -mtime -7 2>/dev/null | while read -r f; do
    grep -oE '<testcase[^>]*name="[^"]+"' "$f" | sed 's/.*name="//;s/"$//' | sort -u | sed "s|^|ALL |"
    grep -B0 -A2 '<failure' "$f" | grep -oE 'name="[^"]+"' | sed 's/name="//;s/"$//' | sort -u | sed "s|^|FAIL |"
  done | sort | uniq -c | awk '
    $2=="FAIL" {fail[$3]=$1}
    $2=="ALL"  {all[$3]=$1}
    END { for (t in fail) if (all[t] > fail[t]) printf "flip-rate %d/%d %s\n", fail[t], all[t], t }
  ' > "$FINDINGS"
  [ -s "$FINDINGS" ] && return 10 || return 0
}

JOB_PROMPT='Eres el flake-warden. Por cada test con flip-rate de los
hallazgos: (1) CUARENTENA INMEDIATA — PR que marca el test como skip
con link a un issue nuevo que documenta el flip-rate y el dueño (el
autor del test según git blame); (2) root-cause en el mismo PR si es
uno de los 3 peores: reproduce corriendo el test 30 veces, identifica
la causa (sleep, orden, red, tiempo) y propone el fix con evidencia de
30 corridas verdes. Un flake escondido con retry infinito es deuda;
la cuarentena visible no.'
