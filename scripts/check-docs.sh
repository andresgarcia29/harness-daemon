#!/usr/bin/env bash
# Contratos documentales que antes divergieron del código. Si cambia una
# capacidad, este check obliga a actualizar código y documentación juntos.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALLER="${1:-${INSTALLER_DIR:-$ROOT/../harness-installer}}"
fail=0

must_contain() {
  grep -qF -- "$2" "$1" || { echo "❌ $1 no contiene: $2"; fail=1; }
}
must_not_contain() {
  if grep -qF -- "$2" "$1"; then echo "❌ $1 conserva texto obsoleto: $2"; fail=1; fi
}

ui_port="$(sed -n 's/^const DefaultUIPort = \([0-9][0-9]*\)$/\1/p' "$ROOT/internal/config/config.go")"
legacy_port="$(sed -n 's/^const defaultPort = \([0-9][0-9]*\)$/\1/p' "$ROOT/cmd/harnessd/main.go")"
[ -n "$ui_port" ] || { echo "❌ no pude derivar DefaultUIPort"; exit 1; }
[ -n "$legacy_port" ] || { echo "❌ no pude derivar defaultPort"; exit 1; }
must_contain "$ROOT/README.md" "127.0.0.1:$ui_port"
must_contain "$ROOT/README.md" "\`$legacy_port\` por compatibilidad"
must_not_contain "$ROOT/README.md" "v0.1 — esqueleto"
must_contain "$ROOT/docs/adr/ADR-0009-el-daemon-observa-no-ejecuta.md" "SUPERSEDED_IN_PART"
must_contain "$ROOT/docs/adr/ADR-0006-auto-update.md" "PARTIALLY_IMPLEMENTED"

if [ -d "$INSTALLER/templates" ]; then
  must_not_contain "$INSTALLER/README.md" "127.0.0.1:7717"
  must_not_contain "$INSTALLER/README.md" "exactamente diez"
  must_contain "$INSTALLER/templates/docs/index.md.tmpl" "harness/evidence.md"
  must_contain "$INSTALLER/templates/docs/index.md.tmpl" "harness/policy.md"
fi

[ "$fail" -eq 0 ] || exit 1
echo "✅ documentación alineada con puertos, capacidades, ADRs y contratos v1"
