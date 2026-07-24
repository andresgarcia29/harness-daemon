#!/usr/bin/env bash
# harness-bug.sh: el canal de vuelta al plugin. Un bug del HARNESS (no de tu
# código) se verifica aquí y se levanta como issue en el repo de harness-creator.
#
# POR QUÉ EXISTE: el harness corre en la máquina de cada usuario, y sus fallas
# mueren ahí. Un agente que tropieza con un bug del propio harness y solo lo
# rodea con un workaround local condena a los demás a tropezar igual. Pero el
# camino contrario (un agente abriendo issues cada vez que algo le sale rojo)
# es peor: ruido que entierra los reportes reales. Este script es el filtro
# determinista entre las dos cosas. El juicio ("¿vale la pena arreglarlo?") lo
# pone el agente siguiendo .claude/skills/harness-bug-report; los hechos
# verificables los pone este archivo.
#
# Uso:
#   harness-bug.sh check <ruta>            ¿es artefacto del plugin y está sin tocar?
#   harness-bug.sh report --title "..." --file <ruta> --repro <archivo> \
#                         --impact "<a quién más le pasa>" [--dry-run] [--force]
#   harness-bug.sh list                    el ledger local de lo ya reportado
#
# LEYES:
#   · FAIL-CLOSED. Cualquier verificación que no pase, NO abre issue. Un
#     reporte falso cuesta más que un bug no reportado: quema la confianza
#     del canal entero.
#   · REDACTA ANTES DE PUBLICAR. El repro suele ser la salida de un comando,
#     y esa salida trae tokens. La ley de secretos también aplica aquí, y
#     aquí es peor: esto sale a un repo PÚBLICO.
#   · CUOTA Y DEDUPE. Máximo 3 issues automáticos por 24h y jamás dos veces
#     el mismo fingerprint (local + búsqueda remota).
#   · APAGABLE. HARNESS_UPSTREAM_ISSUES=off o `upstream_issues: off` en
#     harness-answers.yaml y este script no publica nada.
#
# Portabilidad: bash 3.2, BSD userland, jq. Red solo vía gh.
set -u

UPSTREAM_REPO="${HARNESS_UPSTREAM_REPO:-andresgarcia29/harness-creator}"
WS="$(cd "$(dirname "$0")/.." && pwd)"
LEDGER="$WS/.harness/upstream-issues.jsonl"
QUOTA="${HARNESS_BUG_QUOTA:-3}"

die()  { echo "❌ $1" >&2; exit "${2:-1}"; }
note() { echo "   ↳ $1"; }

sha() {  # sha256 del stdin, portable (shasum en mac, sha256sum en linux)
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 | awk '{print $1}'
  else sha256sum | awk '{print $1}'; fi
}

redact() {
  if [ -f "$WS/scripts/emit.sh" ]; then
    # el bus ya tiene los patrones y los tiene TESTEADOS (test_emit.sh); no
    # se duplican aquí: un segundo juego de regex es un segundo juego de bugs
    # shellcheck disable=SC1091
    . "$WS/scripts/emit.sh" 2>/dev/null && command -v _emit_redact >/dev/null 2>&1 \
      && { _emit_redact; return 0; }
  fi
  cat
}

ver_lt() {  # ver_lt <a> <b> → 0 si a < b (semver simple, sin pre-releases)
  awk -v a="$1" -v b="$2" 'BEGIN{
    na=split(a,x,"."); nb=split(b,y,".")
    n = na>nb ? na : nb
    for(i=1;i<=n;i++){ xi=x[i]+0; yi=y[i]+0
      if(xi<yi) exit 0
      if(xi>yi) exit 1 }
    exit 1 }'
}

# ── propiedad del artefacto ────────────────────────────────────────────────
# Solo lo que el PLUGIN escribe puede ser un bug del plugin. Lo que escribe tu
# instancia (docs, specs, agentes, pasos custom, answers) es tuyo: si falla,
# el bug es local aunque duela igual. Esta tabla es la misma clasificación de
# propiedad que usa /harness-update para decidir quién gana un diff.
owner_of() {
  case "$1" in
    scripts/smoke/*|scripts/cronjobs/jobs/local-*) echo instance ;;
    scripts/*.sh|scripts/*.py|scripts/ui/*|scripts/cronjobs/*) echo plugin ;;
    .claude/hooks/*.sh) echo plugin ;;
    .claude/commands/*.md) echo plugin ;;
    .claude/skills/skill-creator/*|.claude/skills/pipeline-step-creator/*|.claude/skills/harness-bug-report/*) echo plugin ;;
    harness-policy.json|Makefile|AGENTS.md) echo plugin ;;
    *) echo instance ;;
  esac
}

# Ruta del template que originó el artefacto, si es una COPIA literal. Los
# archivos que el generador instancia con placeholders (secrets.sh, ship.sh,
# commands, CLAUDE.md) no tienen contraparte comparable: ahí el drift local es
# esperado y la comparación no aplica.
tpl_for() {
  local p="$1" base; base="$(basename "$p")"
  case "$p" in
    scripts/cronjobs/cron-runner.sh) echo "templates/cronjobs/cron-runner.sh" ;;
    scripts/cronjobs/jobs/*)         echo "templates/cronjobs/jobs/$base" ;;
    scripts/ui/*)                    echo "templates/ui/${p#scripts/ui/}" ;;
    scripts/*)                       echo "templates/scripts/$base" ;;
    .claude/hooks/*)                 echo "templates/hooks/$base" ;;
    .claude/skills/*/SKILL.md)       p="${p#.claude/skills/}"; echo "templates/skills/${p%/SKILL.md}/SKILL.md" ;;
    harness-policy.json)             echo "templates/policy.json" ;;
    *)                               echo "" ;;
  esac
}

# drift_of <ruta> → "igual" | "distinto" | "no-verificable"
drift_of() {
  local p="$1" tpl root
  root="${CLAUDE_PLUGIN_ROOT:-}"
  [ -n "$root" ] && [ -f "$WS/$p" ] || { echo "no-verificable"; return 0; }
  tpl="$(tpl_for "$p")"
  [ -n "$tpl" ] && [ -f "$root/$tpl" ] || { echo "no-verificable"; return 0; }
  if [ "$(sha < "$WS/$p")" = "$(sha < "$root/$tpl")" ]; then echo "igual"; else echo "distinto"; fi
}

enabled() {
  case "${HARNESS_UPSTREAM_ISSUES:-}" in off|false|0) return 1 ;; esac
  if [ -f "$WS/harness-answers.yaml" ]; then
    case "$(grep -E '^upstream_issues:' "$WS/harness-answers.yaml" | head -1 | awk '{print $2}')" in
      off|false) return 1 ;;
    esac
  fi
  return 0
}

# ── check ──────────────────────────────────────────────────────────────────
cmd_check() {
  local p="${1:?uso: harness-bug.sh check <ruta-relativa-al-workspace>}"
  p="${p#./}"; p="${p#"$WS"/}"
  [ -e "$WS/$p" ] || die "no existe en el workspace: $p" 1
  local own drift; own="$(owner_of "$p")"; drift="$(drift_of "$p")"
  echo "artefacto: $p"
  echo "propiedad: $own"
  echo "drift:     $drift"
  if [ "$own" != "plugin" ]; then
    echo "veredicto: NO reportable upstream (es artefacto de tu instancia)"
    note "arréglalo aquí; si crees que el GENERADOR lo produjo mal, reporta el generador, no el archivo"
    return 3
  fi
  if [ "$drift" = "distinto" ]; then
    echo "veredicto: personalizado localmente: upstream NO lo reproduce tal cual"
    note "reproduce contra el archivo original antes de reportar, o usa --force --justification '<por qué el parche local es irrelevante>'"
    return 7
  fi
  echo "veredicto: reportable (artefacto del plugin$([ "$drift" = igual ] && echo ", idéntico al template"))"
  return 0
}

# ── report ─────────────────────────────────────────────────────────────────
cmd_report() {
  local title="" file="" repro="" impact="" just="" dry=0 force=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --title) title="${2:-}"; shift 2 ;;
      --file)  file="${2:-}";  shift 2 ;;
      --repro) repro="${2:-}"; shift 2 ;;
      --impact) impact="${2:-}"; shift 2 ;;
      --justification) just="${2:-}"; shift 2 ;;
      --dry-run) dry=1; shift ;;
      --force)   force=1; shift ;;
      *) die "flag desconocido: $1" 1 ;;
    esac
  done
  [ -n "$title" ]  || die "falta --title" 1
  [ -n "$file" ]   || die "falta --file (el artefacto del harness que falla)" 1
  [ -n "$repro" ]  || die "falta --repro <archivo con el repro mínimo y su salida>" 4
  [ -n "$impact" ] || die "falta --impact (a quién más le pasa; si no le pasa a nadie más, no es upstream)" 1

  enabled || die "reportes upstream deshabilitados en esta instancia (upstream_issues: off)" 8

  file="${file#./}"; file="${file#"$WS"/}"
  [ -e "$WS/$file" ] || die "no existe en el workspace: $file" 1

  # 1 · propiedad y drift (fail-closed)
  local own drift; own="$(owner_of "$file")"; drift="$(drift_of "$file")"
  [ "$own" = "plugin" ] || die "$file es artefacto de tu instancia, no del plugin: no hay bug upstream que reportar" 3
  if [ "$drift" = "distinto" ] && [ "$force" -ne 1 ]; then
    die "$file está personalizado localmente: upstream no lo reproduce tal cual" 7
  fi
  [ "$drift" = "distinto" ] && [ -z "$just" ] && [ "$force" -eq 1 ] && \
    die "--force sobre un archivo con drift exige --justification" 7

  # 2 · repro con contenido (un reporte sin repro es una queja)
  [ -f "$WS/$repro" ] || [ -f "$repro" ] || die "no existe el archivo de repro: $repro" 4
  local repro_path="$repro"; [ -f "$WS/$repro" ] && repro_path="$WS/$repro"
  [ -s "$repro_path" ] || die "el repro está vacío: $repro" 4

  # 3 · versión: reportar un bug ya arreglado upstream es la falla más común
  local local_ver up_ver=""
  local_ver="$(cat "$WS/.harness-version" 2>/dev/null | tr -d ' \n')"
  if command -v gh >/dev/null 2>&1 && [ "$dry" -eq 0 ]; then
    up_ver="$(gh api "repos/$UPSTREAM_REPO/contents/.claude-plugin/plugin.json" \
      -H "Accept: application/vnd.github.raw" 2>/dev/null | jq -r '.version // empty' 2>/dev/null)"
  fi
  if [ -n "$local_ver" ] && [ -n "$up_ver" ] && ver_lt "$local_ver" "$up_ver" && [ "$force" -ne 1 ]; then
    die "tu instancia está en $local_ver y upstream va en $up_ver: actualiza (/harness-update) y re-verifica antes de reportar" 6
  fi

  # 4 · fingerprint y dedupe local
  local norm fp
  norm="$(printf '%s' "$title" | tr '[:upper:]' '[:lower:]' | tr -cs '[:alnum:]' ' ' | awk '{$1=$1};1')"
  fp="$(printf '%s|%s' "$file" "$norm" | sha | cut -c1-12)"
  if [ -f "$LEDGER" ] && grep -q "\"fp\":\"$fp\"" "$LEDGER" 2>/dev/null; then
    local prev; prev="$(grep "\"fp\":\"$fp\"" "$LEDGER" | tail -1 | jq -r '.url // "(sin url)"' 2>/dev/null)"
    echo "⏭  ya reportado (fp $fp): $prev"
    return 0
  fi

  # 5 · cuota diaria (una tormenta de issues automáticos es spam, no señal)
  if [ -f "$LEDGER" ]; then
    local recent now; now="$(date +%s)"
    recent="$(jq -r --argjson now "$now" 'select((.epoch // 0) > ($now - 86400)) | .fp' "$LEDGER" 2>/dev/null | wc -l | tr -d ' ')"
    if [ "${recent:-0}" -ge "$QUOTA" ]; then
      die "cuota de reportes automáticos agotada ($recent en 24h, tope $QUOTA): junta los hallazgos en UN issue o repórtalo a mano" 5
    fi
  fi

  # 6 · cuerpo, redactado SIEMPRE
  local body os bashv jqv
  os="$(uname -sr 2>/dev/null)"; bashv="${BASH_VERSION:-?}"; jqv="$(jq --version 2>/dev/null)"
  body="$(cat <<EOF
### Qué falla

$title

### Artefacto del harness

\`$file\` (propiedad del plugin; comparación con el template: $drift)
$([ -n "$just" ] && printf '\nJustificación del drift local: %s\n' "$just")
### Repro mínimo (y su salida)

\`\`\`
$(head -c 12000 "$repro_path" | head -120)
\`\`\`

### A quién más le pasa

$impact

### Entorno

- Instancia: \`.harness-version\` = ${local_ver:-desconocida}${up_ver:+ (upstream: $up_ver)}
- OS: ${os:-?} · bash ${bashv} · ${jqv:-jq ausente}

### Verificación previa (determinista, la hizo \`scripts/harness-bug.sh\`)

- Artefacto es propiedad del plugin, no personalización de la instancia: ✅
- Comparación contra el template del plugin: $drift
- Instancia al día contra upstream: $([ -n "$up_ver" ] && { ver_lt "${local_ver:-0}" "$up_ver" && echo "NO (forzado)" || echo "✅"; } || echo "no verificado (sin red)")
- Repro adjunto y no vacío: ✅
- Redacción de secretos aplicada al reporte: ✅

<!-- harness-fp: $fp -->
Levantado automáticamente por el harness (\`scripts/harness-bug.sh\`) desde una instancia instalada.
EOF
)"
  body="$(printf '%s' "$body" | redact)"

  if [ "$dry" -eq 1 ]; then
    echo "── DRY RUN · fp $fp · repo $UPSTREAM_REPO ──"
    echo "título: [harness] $title"
    echo "$body"
    return 0
  fi

  # 7 · publicar (gh es el único canal; sin él, deja el reporte listo a mano)
  command -v gh >/dev/null 2>&1 || die "gh no instalado: no puedo abrir el issue. Cuerpo listo con --dry-run; súbelo a https://github.com/$UPSTREAM_REPO/issues/new" 2
  gh auth status >/dev/null 2>&1 || die "gh sin autenticar (gh auth login): no puedo abrir el issue" 2

  # dedupe remoto: alguien más (u otra máquina tuya) pudo reportarlo ya
  local dup
  dup="$(gh issue list --repo "$UPSTREAM_REPO" --state all --search "$fp" --limit 3 --json url --jq '.[0].url' 2>/dev/null)"
  if [ -n "$dup" ] && [ "$dup" != "null" ]; then
    echo "⏭  ya existe upstream (fp $fp): $dup"
    ledger_add "$fp" "$file" "$dup" "duplicado"
    return 0
  fi

  local tmp url; tmp="$(mktemp)"; printf '%s\n' "$body" > "$tmp"
  url="$(gh issue create --repo "$UPSTREAM_REPO" --title "[harness] $title" \
        --body-file "$tmp" --label bug 2>/dev/null)" \
    || url="$(gh issue create --repo "$UPSTREAM_REPO" --title "[harness] $title" --body-file "$tmp" 2>/dev/null)"
  rm -f "$tmp"
  [ -n "$url" ] || die "gh no pudo crear el issue (¿permisos? ¿issues deshabilitados?)" 2

  ledger_add "$fp" "$file" "$url" "creado"
  echo "✅ issue upstream: $url"
  [ -f "$WS/scripts/emit.sh" ] && bash "$WS/scripts/emit.sh" decision \
    "bug del harness reportado upstream: $title ($url)" "" "${HARNESS_TASK:-${TASK:-}}" 2>/dev/null
  return 0
}

ledger_add() {  # fp file url estado
  mkdir -p "$(dirname "$LEDGER")" 2>/dev/null || return 0
  command -v jq >/dev/null 2>&1 || return 0
  jq -nc --arg fp "$1" --arg f "$2" --arg u "$3" --arg st "$4" \
     --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson ep "$(date +%s)" \
     '{ts:$ts,epoch:$ep,fp:$fp,file:$f,url:$u,status:$st}' >> "$LEDGER" 2>/dev/null || true
}

cmd_list() {
  [ -f "$LEDGER" ] || { echo "sin reportes upstream registrados"; return 0; }
  jq -r '"\(.ts)  \(.status)  \(.file)  \(.url)"' "$LEDGER" 2>/dev/null
}

case "${1:-}" in
  check)  shift; cmd_check "$@" ;;
  report) shift; cmd_report "$@" ;;
  list)   shift; cmd_list "$@" ;;
  *) echo "uso: harness-bug.sh <check|report|list> …" >&2; exit 1 ;;
esac
