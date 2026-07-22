#!/usr/bin/env bash
# doctor.sh — Fase 4 y chequeo de salud continuo. Determinista, cero tokens.
# Todos los checks se DERIVAN de harness-answers.yaml (esquema fijo):
#   bin: <x>   → command -v <x>
#   mcp: <x>   → clave presente en .mcp.json
#   env://X    → variable presente en el entorno (warn)
# Cada fallo imprime su remediación exacta.
# Portabilidad: bash 3.2 (macOS), BSD grep. Requiere jq.
set -uo pipefail

WS="${1:-.}"; WS="$(cd "$WS" && pwd)"
ANSWERS="$WS/harness-answers.yaml"
FAIL=0; WARN=0

ok()   { echo "✅ $1"; }
warn() { echo "⚠️  $1"; WARN=$((WARN+1)); }
fail() { echo "❌ $1"; echo "   ↳ remediación: $2"; FAIL=$((FAIL+1)); }

echo "── doctor: $WS ──"

command -v jq >/dev/null || { fail "jq no instalado" "brew install jq | apt-get install -y jq"; echo "── resultado: $FAIL fallos ──"; exit 1; }

# 1 · Archivos núcleo
for f in CLAUDE.md manifest.yaml harness-answers.yaml .harness-version inventory.json; do
  [ -f "$WS/$f" ] && ok "$f presente" || fail "$f faltante" "corre /harness-init de nuevo"
done

# 1b · Coherencia manifest ↔ repos/ (fantasmas y faltantes)
if [ -f "$WS/manifest.yaml" ] && [ -d "$WS/repos" ]; then
  for name in $(grep -E '^[[:space:]]+- name:' "$WS/manifest.yaml" | awk '{print $3}'); do
    [ -d "$WS/repos/$name/.git" ] || warn "repo en manifest sin clon: $name — clónalo o quítalo del manifest/DAG"
  done
  for d in "$WS/repos"/*/; do
    [ -d "$d/.git" ] || continue
    name="$(basename "$d")"
    grep -qE "^[[:space:]]+- name: $name\$" "$WS/manifest.yaml" \
      || warn "clon sin registrar en manifest: repos/$name — regístralo o remuévelo (¿repo fantasma?)"
  done
fi

# 2 · Scripts de instancia ejecutables
for s in ship.sh worktree-task.sh quiet.sh with-secrets.sh emit.sh          build-slot.sh gowork.sh py.sh fe.sh repo-brief.sh          stamp-models.sh graph-refresh.sh pull-all.sh skills-sync.sh          verdict-scaffold.sh; do
  if [ -f "$WS/scripts/$s" ]; then
    [ -x "$WS/scripts/$s" ] && ok "scripts/$s ejecutable" || fail "scripts/$s no ejecutable" "chmod +x scripts/$s"
    bash -n "$WS/scripts/$s" 2>/dev/null && ok "scripts/$s sintaxis válida" || fail "scripts/$s con error de sintaxis" "revisa el archivo (bash -n scripts/$s)"
  else
    fail "scripts/$s faltante" "corre /harness-init de nuevo"
  fi
done

# 3 · .mcp.json válido y coherente con answers
if [ -f "$WS/.mcp.json" ]; then
  if jq empty "$WS/.mcp.json" 2>/dev/null; then
    ok ".mcp.json JSON válido"
    if [ -f "$ANSWERS" ]; then
      for m in $(grep -E '^[[:space:]]+mcp:' "$ANSWERS" | awk '{print $2}' | sort -u); do
        jq -e --arg m "$m" '.mcpServers[$m]' "$WS/.mcp.json" >/dev/null 2>&1 \
          && ok "mcp configurado: $m" \
          || fail "mcp '$m' elegido pero ausente en .mcp.json" "agrega la entrada según catalog/capabilities.yaml o quítalo de harness-answers.yaml"
      done
    fi
  else
    fail ".mcp.json inválido" "revisa la sintaxis JSON"
  fi
fi

# 4 · Hook de protección de main registrado y ejecutable
if [ -f "$WS/.claude/settings.json" ]; then
  grep -q "block-direct-push" "$WS/.claude/settings.json" 2>/dev/null \
    && ok "hook block-direct-push registrado" \
    || warn "hook block-direct-push no registrado en .claude/settings.json — git push directo a main NO está bloqueado"
  if [ -f "$WS/.claude/hooks/block-direct-push.sh" ]; then
    [ -x "$WS/.claude/hooks/block-direct-push.sh" ] && ok "hook block-direct-push ejecutable" || fail "hook block-direct-push no ejecutable" "chmod +x .claude/hooks/block-direct-push.sh"
  fi
else
  warn ".claude/settings.json faltante — sin hooks de protección"
fi

# 5 · Links del CLAUDE.md resuelven
if [ -f "$WS/CLAUDE.md" ]; then
  broken=0
  while read -r p; do
    [ -e "$WS/$p" ] || { warn "link roto en CLAUDE.md: $p"; broken=$((broken+1)); }
  done < <(grep -oE '(docs|scripts|\.claude)/[A-Za-z0-9._/\-]+' "$WS/CLAUDE.md" | sort -u)
  [ "$broken" -eq 0 ] && ok "links del CLAUDE.md resuelven"
fi

# 6 · CLIs seleccionadas (bin: del answers). scope: cronjob degrada a
#     warning cuando falta — solo lo usa un detector de cronjob.
if [ -f "$ANSWERS" ]; then
  while read -r bin scope; do
    [ -n "$bin" ] || continue
    if command -v "$bin" >/dev/null; then
      ok "cli: $bin"
    elif [ "$scope" = "cronjob" ]; then
      warn "cli faltante: $bin (scope: cronjob — instálalo al activar su cronjob)"
    else
      fail "cli faltante: $bin" "corre scripts/bootstrap.sh (instala todo lo elegido) o ver install en catalog/capabilities.yaml"
    fi
  done < <(awk '
    /^  - name:/ { if (bin != "") print bin, scope; bin=""; scope="core" }
    /^    bin:/   { bin=$2 }
    /^    scope:/ { scope=$2 }
    END { if (bin != "") print bin, scope }
  ' "$ANSWERS" | sort -u)
else
  warn "sin harness-answers.yaml — no puedo verificar CLIs ni MCPs elegidos"
fi

# 7 · Secretos: flujo completo, nunca valores
if [ -f "$ANSWERS" ]; then
  # referencias env://: presencia de la variable
  for var in $(grep -oE 'env://[A-Za-z_][A-Za-z0-9_]*' "$ANSWERS" | sed 's|env://||' | sort -u); do
    [ -n "${!var:-}" ] && ok "secreto presente: \$$var" || warn "secreto no presente en entorno: \$$var"
  done
  # fuente vault/gcp-sm: bootstrap (token) y materialización (.secrets)
  src="$(grep -E '^[[:space:]]+source:' "$ANSWERS" | head -1 | awk '{print $2}')"
  if [ "$src" = "vault" ]; then
    tokfile="$HOME/.config/harness/vault-token"
    if [ ! -f "$tokfile" ]; then
      warn "sin token de Vault — corre scripts/bootstrap.sh (te lo pide interactivo, fuera del chat)"
    else
      # VIGENCIA, no solo presencia: un token muerto es peor que ninguno
      vaddr="$(grep -E '^[[:space:]]+vault_addr:' "$ANSWERS" | head -1 | awk '{print $2}' | tr -d '"')"
      if command -v vault >/dev/null && [ -n "$vaddr" ]; then
        if VAULT_ADDR="$vaddr" VAULT_TOKEN="$(cat "$tokfile")" vault token lookup >/dev/null 2>&1; then
          ok "token de Vault VÁLIDO"
        else
          warn "token de Vault presente pero EXPIRADO/sin permisos (o Vault inaccesible)"
          echo "   ↳ renovación: export VAULT_ADDR=$vaddr && vault login -method=<tu método>"
          echo "     luego: make init (te pide el token nuevo, lo valida y materializa .secrets)"
          echo "     detalle completo: README.md § Secretos"
        fi
      else
        ok "token de Vault presente (sin validar: falta vault CLI o vault_addr en answers)"
      fi
    fi
  fi
  if [ "$src" = "vault" ] || [ "$src" = "gcp-secret-manager" ]; then
    [ -f "$WS/.secrets" ] && ok ".secrets materializado" \
      || warn ".secrets no materializado — corre: scripts/secrets.sh pull (los MCPs autenticados y deploy-watch lo necesitan)"
  fi
fi

# 8 · Agentes y comandos de pipeline
agents=$(ls "$WS"/.claude/agents/*.md 2>/dev/null | wc -l | tr -d ' ')
[ "$agents" -gt 0 ] && ok "$agents agentes en .claude/agents/" || fail "sin agentes en .claude/agents/" "corre /harness-init de nuevo"
for c in feature rfc implement review ship; do
  [ -f "$WS/.claude/commands/$c.md" ] && ok "comando /$c presente" || warn "comando /$c faltante — el pipeline documentado en CLAUDE.md no está completo"
done
[ -f "$WS/.claude/commands/auto.md" ] && ok "comando /auto presente (pipeline autónomo: ticket o prompt → prod)" \
  || warn "comando /auto faltante — sin él no hay pipeline sin intervención; corre /harness-init . (modo update)"

# 8a-bis · El bus del harness
if [ -f "$WS/scripts/emit.sh" ]; then
  ok "scripts/emit.sh presente (el bus: gates, fases, supuestos, paradas)"
  grep -q "emit.sh" "$WS/scripts/ship.sh" 2>/dev/null \
    || warn "ship.sh no sourcea emit.sh — los gates no se cuentan y el panel no puede enseñar cuándo el harness frenó a su propio agente"
else
  warn "falta scripts/emit.sh — el panel solo verá agentes y tokens (lo que presta Claude Code), nunca tus decisiones ni tus gates; corre /harness-init . (modo update)"
fi

# 8b · Presupuesto de contexto SIEMPRE inyectado.
# CLAUDE.md + constitution.md entran en CADA agente, en CADA turno, para siempre.
# Nadie los borra y cada versión les suma una lección. El límite no es el tamaño
# de la ventana: es el "context rot" — la atención se degrada mucho antes de
# llenarla, y un mapa de 3k líneas se ignora entero. Un techo medido es la
# diferencia entre una ley que se lee y una que se saltan.
ctx_words=0
for f in "$WS/CLAUDE.md" "$WS/docs/constitution.md"; do
  [ -f "$f" ] && ctx_words=$((ctx_words + $(wc -w < "$f" | tr -d ' ')))
done
# ~1.3 tokens por palabra (aprox. honesta; la cuenta exacta la da count_tokens)
ctx_tokens=$((ctx_words * 13 / 10))
if [ "$ctx_tokens" -gt 3000 ]; then
  fail "contexto siempre-inyectado ≈ ${ctx_tokens} tokens (CLAUDE.md + constitution.md)" \
       "PASA de 3000. Es un MAPA, no un manual: mueve el detalle a docs/ o a una skill y deja punteros. Un CLAUDE.md inflado hace que los agentes ignoren las instrucciones que sí importan."
elif [ "$ctx_tokens" -gt 1500 ]; then
  warn "contexto siempre-inyectado ≈ ${ctx_tokens} tokens — vigila el techo (falla a 3000). Prueba: ¿quitar esta línea haría que un agente se equivoque? Si no, fuera."
else
  ok "contexto siempre-inyectado ≈ ${ctx_tokens} tokens (bajo el techo de 1500)"
fi

# 8c · La UI (observa local; opera solo creando trabajo — ADR-0010)
if [ -f "$WS/scripts/ui/server.py" ]; then
  ok "panel local presente (make ui)"
  grep -q "ui-emit.sh" "$WS/.claude/settings.json" 2>/dev/null \
    || warn "ui-emit.sh no está registrado en .claude/settings.json — el panel vivirá de tasks/ y transcripts, sin el bus de eventos del harness"
  grep -q "track-read.sh" "$WS/.claude/settings.json" 2>/dev/null \
    || warn "track-read.sh no está registrado — sin él, ship.sh no puede verificar la evidencia de la compliance matrix (gate_evidence queda ciego)"
  command -v claude >/dev/null 2>&1 \
    || warn "el CLI 'claude' no está en PATH — el plano de OPERAR del panel (Nueva tarea, responder a un agente) lanza 'claude -p' headless y sin él esos botones devolverán error (observar sigue funcionando)"
fi

# 8d · Bits de ejecución: un hook sin +x falla EN SILENCIO (Claude Code no
# puede ejecutarlo y nadie te lo dice). La suite del instalador cachó seis
# templates así; aquí vigilamos la instancia instalada.
for f in "$WS"/scripts/*.sh "$WS"/.claude/hooks/*.sh; do
  [ -f "$f" ] || continue
  [ -x "$f" ] || warn "$(basename "$f") no es ejecutable — chmod +x ${f#"$WS"/} (un hook sin +x observa nada y un script sin +x muere en el primer uso)"
done

# 9 · Constituciones DRAFT pendientes de ratificar
drafts=$(grep -l "status: DRAFT" "$WS"/.claude/agents/*.md "$WS"/docs/constitution.md "$WS"/specs/*/spec.md 2>/dev/null | wc -l | tr -d ' ')
[ "$drafts" -gt 0 ] && warn "$drafts documentos en DRAFT (constituciones/constitution/specs) — ratificar antes del primer RFC"

for p in harness-policy.py evidence.py; do
  if [ -f "$WS/scripts/$p" ]; then
    python3 -m py_compile "$WS/scripts/$p" 2>/dev/null && ok "scripts/$p compila" || fail "scripts/$p con error de sintaxis" "revisa el archivo (python3 -m py_compile scripts/$p)"
  else
    fail "scripts/$p faltante" "corre el update de la instancia (harness update o /harness-init .)"
  fi
done

# Frescura de clones: explorar un clon podrido fue el error más caro medido
# en campo (27 commits atrás = inventarios de endpoints inexistentes).
if [ -d "$WS/repos" ]; then
  old_fetch=0; total_r=0
  now_s=$(date +%s)
  for r in "$WS"/repos/*/; do
    [ -d "$r/.git" ] || continue
    total_r=$((total_r+1))
    fh="$r/.git/FETCH_HEAD"
    if [ ! -f "$fh" ] || [ $(( now_s - $(stat -f %m "$fh" 2>/dev/null || stat -c %Y "$fh" 2>/dev/null || echo 0) )) -gt 172800 ]; then
      old_fetch=$((old_fetch+1))
    fi
  done
  if [ "$total_r" -gt 0 ] && [ $((old_fetch * 2)) -gt "$total_r" ]; then
    warn "clones posiblemente podridos: $old_fetch/$total_r sin fetch en 48h — corre make pull antes de explorar"
  fi
fi

# 10 · Capa SDD y modelos
[ -f "$WS/docs/constitution.md" ] && ok "constitution.md presente" || warn "sin docs/constitution.md — los agentes no tienen tie-breaker"
[ -f "$WS/models.yaml" ] && ok "models.yaml presente" || warn "sin models.yaml — sin política de ruteo/escalación de modelos"
if [ -x "$WS/scripts/stamp-models.sh" ] && [ -f "$WS/models.yaml" ]; then
  if bash "$WS/scripts/stamp-models.sh" check >/dev/null 2>&1; then
    ok "agentes alineados con models.yaml (provider + aliases)"
  else
    fail "frontmatter de agentes desalineado con models.yaml" "make models (re-estampa desde la política)"
  fi
fi
if [ -f "$WS/skills.yaml" ] && [ -x "$WS/scripts/skills-sync.sh" ]; then
  if bash "$WS/scripts/skills-sync.sh" --check >/dev/null 2>&1; then
    ok "capa compartida de skills en sync (skills.yaml)"
  else
    warn "skills compartidas con drift o fuente inaccesible; corre: make skills"
  fi
fi
[ -f "$WS/AGENTS.md" ] && ok "AGENTS.md presente (mapa multi-herramienta)" || warn "sin AGENTS.md — Cursor/Kimi/otros agentes no tienen punto de entrada"
# Integridad de hooks: TODO hook referenciado en settings.json debe existir y
# ser ejecutable. Un hook registrado pero ausente spamea "not found" en CADA
# tool call del agente (visto en un VPS real: el generador olvidó un archivo).
# Intersección de conjuntos: cero opinión.
if [ -f "$WS/.claude/settings.json" ]; then
  while IFS= read -r h; do
    [ -n "$h" ] || continue
    hp="$WS/${h#\$CLAUDE_PROJECT_DIR/}"; hp="${hp//\"/}"
    if [ ! -f "$hp" ]; then
      fail "hook registrado en settings.json pero AUSENTE: $h" "re-corre el update de la instancia (harness update o /harness-init .) o copia templates/hooks/$(basename "$h") del plugin"
    elif [ ! -x "$hp" ]; then
      fail "hook registrado pero no ejecutable: $h" "chmod +x $hp"
    fi
  done <<EOF
$(jq -r '.. | .command? // empty' "$WS/.claude/settings.json" 2>/dev/null | grep -o '[^ "]*\.claude/hooks/[^ "]*\.sh' | sed "s|.*\.claude/hooks/|.claude/hooks/|" | sort -u)
EOF
fi

# beads: TODO el pipeline de implement ordena por `bd ready --json` — si el
# workspace no está inicializado, /auto muere en la primera consulta del DAG.
if command -v bd >/dev/null 2>&1; then
  if (cd "$WS" && bd ready --json >/dev/null 2>&1); then
    ok "beads operativo (bd ready responde — el DAG de tareas tiene motor)"
  else
    warn "bd instalado pero 'bd ready --json' falla en el workspace — inicializa beads (bd init) o el pipeline no puede ordenar el DAG"
  fi
fi
if command -v graphify >/dev/null 2>&1; then
  if [ -f "$WS/graphify-out/graph.json" ] || [ -f "$WS/repos/graphify-out/graph.json" ]; then
    ok "grafo de graphify construido (graphify query responde de verdad)"
  else
    warn "graphify instalado pero SIN grafo — 'graphify query' falla y los agentes caen a grep masivo; corre scripts/graph-refresh.sh (o make graph)"
  fi
fi
[ -d "$WS/specs" ] && ok "specs/ presente ($(ls "$WS/specs" 2>/dev/null | wc -l | tr -d ' ') capabilities)" || warn "sin specs/ — los abogados litigan sin documento citable"

# 11 · Cronjobs self-healing
if [ -d "$WS/scripts/cronjobs" ]; then
  [ -x "$WS/scripts/cronjobs/cron-runner.sh" ] && ok "cron-runner.sh ejecutable" || fail "cron-runner.sh no ejecutable" "chmod +x scripts/cronjobs/cron-runner.sh"
  njobs=$(ls "$WS/scripts/cronjobs/jobs/"*.sh 2>/dev/null | wc -l | tr -d ' ')
  [ "$njobs" -gt 0 ] && ok "$njobs cronjobs instalados" || warn "scripts/cronjobs sin jobs"
  # circuit breakers abiertos
  for f in "$WS"/.cache/cron/*.fails; do
    [ -f "$f" ] || continue
    [ "$(cat "$f")" -ge 3 ] && warn "circuit breaker ABIERTO: $(basename "$f" .fails) — revisar y borrar $f"
  done
fi

echo "── resultado: $FAIL fallos, $WARN advertencias ──"
[ "$FAIL" -eq 0 ]
