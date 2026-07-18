#!/usr/bin/env bash
# discover.sh — Fase 1 del harness-init. Determinista, cero tokens.
# Escanea <workspace>/repos (o <workspace> si no existe repos/) y produce
# <workspace>/inventory.json: lenguajes, señales, rol inferido y tamaño
# por repo, más un resumen agrupado por rol (insumo del clustering de
# agentes en la entrevista).
#
# Portabilidad: bash 3.2 (macOS), BSD grep/find. Requiere jq.
set -euo pipefail

WS="${1:?uso: discover.sh <workspace>}"
WS="$(cd "$WS" && pwd)"
REPOS_DIR="$WS/repos"
[ -d "$REPOS_DIR" ] || REPOS_DIR="$WS"

command -v jq >/dev/null || { echo "❌ jq requerido. Remediación: brew install jq | apt-get install -y jq"; exit 1; }

tmp="$(mktemp)"
echo "[]" > "$tmp"

# ¿El package.json declara alguna de estas dependencias?
pkg_has() { # pkg_has <dir> <dep>
  [ -f "$1/package.json" ] && grep -q "\"$2\"" "$1/package.json" 2>/dev/null
}

guess_role() { # guess_role <dir> <name> — imprime el rol en stdout
  local dir="$1" name="$2"

  # contratos: buf o dominancia de .proto
  if [ -f "$dir/buf.yaml" ] || [ -f "$dir/buf.gen.yaml" ]; then echo "contracts"; return; fi

  # infra terraform: module (reutilizable) vs live (raíz aplicable)
  if ls "$dir"/*.tf >/dev/null 2>&1 || find "$dir" -maxdepth 2 -name "*.tf" -print -quit 2>/dev/null | grep -q .; then
    if [ -f "$dir/variables.tf" ] || [ -f "$dir/outputs.tf" ] || echo "$name" | grep -qi "module"; then
      echo "infra-module"
    else
      echo "infra-live"
    fi
    return
  fi

  # mobile
  if [ -f "$dir/pubspec.yaml" ]; then echo "mobile"; return; fi

  # backend: servicio (deployable) vs librería compartida
  if [ -f "$dir/go.mod" ]; then
    if [ -d "$dir/cmd" ] || find "$dir" -maxdepth 2 -name "Dockerfile*" -print -quit 2>/dev/null | grep -q .; then
      echo "service"
    else
      echo "library"
    fi
    return
  fi
  if [ -f "$dir/pyproject.toml" ]; then
    if find "$dir" -maxdepth 2 -name "Dockerfile*" -print -quit 2>/dev/null | grep -q .; then
      echo "service"
    else
      echo "library"
    fi
    return
  fi

  # frontend TS/JS
  if [ -f "$dir/package.json" ]; then
    if pkg_has "$dir" react || pkg_has "$dir" vue || pkg_has "$dir" svelte || pkg_has "$dir" astro || pkg_has "$dir" vite; then
      echo "frontend"
    elif find "$dir" -maxdepth 2 -name "Dockerfile*" -print -quit 2>/dev/null | grep -q .; then
      echo "service"
    else
      echo "library"
    fi
    return
  fi

  # librería de CI reusable (solo workflows)
  if [ -d "$dir/.github/workflows" ]; then echo "ci-library"; return; fi

  # docs: mayoría markdown
  local md_count total_count
  md_count=$(find "$dir" -type f -name "*.md" -not -path "*/.git/*" 2>/dev/null | wc -l | tr -d ' ')
  total_count=$(find "$dir" -type f -not -path "*/.git/*" 2>/dev/null | wc -l | tr -d ' ')
  if [ "$total_count" -gt 0 ] && [ $((md_count * 2)) -ge "$total_count" ]; then echo "docs"; return; fi

  echo "unknown"
}

scan_repo() {
  local dir="$1" name langs=() signals=() has_claude_md=false branch="" remote="" role="" files=0
  name="$(basename "$dir")"

  [ -f "$dir/go.mod" ]         && langs+=("go")
  [ -f "$dir/package.json" ]   && langs+=("typescript")
  [ -f "$dir/pyproject.toml" ] && langs+=("python")
  [ -f "$dir/pubspec.yaml" ]   && langs+=("dart")
  { ls "$dir"/*.tf >/dev/null 2>&1 || find "$dir" -maxdepth 2 -name "*.tf" -print -quit 2>/dev/null | grep -q .; } && langs+=("terraform")

  { [ -f "$dir/buf.yaml" ] || [ -f "$dir/buf.gen.yaml" ]; } && signals+=("buf")
  find "$dir" -maxdepth 3 -name "Chart.yaml" -print -quit 2>/dev/null | grep -q . && signals+=("helm")
  [ -d "$dir/.github/workflows" ] && signals+=("gha")
  find "$dir" -maxdepth 2 -name "Dockerfile*" -print -quit 2>/dev/null | grep -q . && signals+=("docker")
  find "$dir" -maxdepth 3 -name "*.proto" -print -quit 2>/dev/null | grep -q . && signals+=("proto")
  find "$dir" -maxdepth 2 -name "docker-compose*.y*ml" -print -quit 2>/dev/null | grep -q . && signals+=("compose")
  grep -rq "argoproj.io" "$dir" --include="*.yaml" 2>/dev/null && signals+=("argocd")
  grep -rq "kargo.akuity.io" "$dir" --include="*.yaml" 2>/dev/null && signals+=("kargo")
  grep -rq 'provider "google"' "$dir" --include="*.tf" 2>/dev/null && signals+=("gcp")
  [ -f "$dir/CLAUDE.md" ] && has_claude_md=true

  branch="$(git -C "$dir" symbolic-ref --short HEAD 2>/dev/null || echo unknown)"
  remote="$(git -C "$dir" remote get-url origin 2>/dev/null || echo "")"
  role="$(guess_role "$dir" "$name")"
  files=$(find "$dir" -type f -not -path "*/.git/*" 2>/dev/null | wc -l | tr -d ' ')

  jq --arg name "$name" \
     --arg branch "$branch" \
     --arg remote "$remote" \
     --arg role "$role" \
     --argjson files "$files" \
     --argjson claude "$has_claude_md" \
     --argjson langs "$(printf '%s\n' "${langs[@]:-}" | jq -R . | jq -s 'map(select(length>0))')" \
     --argjson signals "$(printf '%s\n' "${signals[@]:-}" | jq -R . | jq -s 'map(select(length>0))')" \
     '. += [{name:$name, current_branch:$branch, remote:$remote, role_guess:$role,
             file_count:$files, languages:$langs, signals:$signals, has_claude_md:$claude}]' \
     "$tmp" > "$tmp.new" && mv "$tmp.new" "$tmp"
}

found=0
for dir in "$REPOS_DIR"/*/; do
  [ -d "$dir/.git" ] || continue
  scan_repo "${dir%/}"
  found=$((found+1))
done

if [ "$found" -eq 0 ]; then
  echo "❌ No se encontraron repos git en $REPOS_DIR"
  echo "   Remediación: clona tus repos en $WS/repos/ y reintenta."
  exit 2
fi

# ── Señales de fuente de secretos (workspace-level, para que la
#    entrevista recomiende con evidencia, no adivine) ─────────────────
secret_hints=()
find "$REPOS_DIR" -maxdepth 3 -name ".sops.yaml" -print -quit 2>/dev/null | grep -q . && secret_hints+=("sops")
find "$REPOS_DIR" -maxdepth 3 \( -name "doppler.yaml" -o -name "doppler.yml" \) -print -quit 2>/dev/null | grep -q . && secret_hints+=("doppler")
find "$REPOS_DIR" -maxdepth 2 -name ".env.example" -print -quit 2>/dev/null | grep -q . && secret_hints+=("env")
grep -rq "VAULT_ADDR\|vault_generic_secret\|hashicorp/vault" "$REPOS_DIR" --include="*.tf" --include="*.yaml" --include="*.md" 2>/dev/null && secret_hints+=("vault")
grep -rq "google_secret_manager_secret" "$REPOS_DIR" --include="*.tf" 2>/dev/null && secret_hints+=("gcp-secret-manager")
grep -rq "aws_secretsmanager" "$REPOS_DIR" --include="*.tf" 2>/dev/null && secret_hints+=("aws-secrets-manager")
grep -rq "op://" "$REPOS_DIR" --include="*.yaml" --include="*.env*" --include="*.tpl" 2>/dev/null && secret_hints+=("1password")
SECRET_HINTS_JSON="$(printf '%s\n' "${secret_hints[@]:-}" | jq -R . | jq -s 'map(select(length>0))')"

jq -n \
  --arg workspace "$WS" \
  --arg scanned_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson repos "$(cat "$tmp")" \
  --argjson secret_hints "$SECRET_HINTS_JSON" \
  '{workspace:$workspace, scanned_at:$scanned_at, repo_count:($repos|length), repos:$repos,
    secret_hints:$secret_hints,
    by_role: ($repos | group_by(.role_guess) | map({key: .[0].role_guess, value: map(.name)}) | from_entries),
    summary:{
      go:[$repos[]|select(.languages|index("go"))|.name],
      typescript:[$repos[]|select(.languages|index("typescript"))|.name],
      python:[$repos[]|select(.languages|index("python"))|.name],
      dart:[$repos[]|select(.languages|index("dart"))|.name],
      terraform:[$repos[]|select(.languages|index("terraform"))|.name],
      proto:[$repos[]|select(.signals|index("proto"))|.name],
      helm:[$repos[]|select(.signals|index("helm"))|.name],
      argocd:[$repos[]|select(.signals|index("argocd"))|.name],
      kargo:[$repos[]|select(.signals|index("kargo"))|.name],
      missing_claude_md:[$repos[]|select(.has_claude_md|not)|.name]
    }}' > "$WS/inventory.json"

echo "✅ inventory.json generado: $found repos escaneados"
echo "── repos por rol (insumo del clustering de agentes) ──"
jq -r '.by_role | to_entries[] | "  \(.key): \(.value | join(", "))"' "$WS/inventory.json"
rm -f "$tmp"
