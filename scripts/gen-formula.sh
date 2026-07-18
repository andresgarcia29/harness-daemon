#!/usr/bin/env bash
# gen-formula.sh <version> — genera Formula/harness.rb para el tap
# andresgarcia29/homebrew-agm a partir de dist/SHA256SUMS.
# Los assets se llaman harnessd-<os>-<arch> (compat con panel.sh);
# la formula instala el binario como `harness` y symlinkea `harnessd`.
set -euo pipefail
VER="${1:?uso: gen-formula.sh <version-sin-v>}"
DIR="$(cd "$(dirname "$0")/.." && pwd)"
SUMS="$DIR/dist/SHA256SUMS"
[ -f "$SUMS" ] || { echo "❌ falta $SUMS — corre make dist primero"; exit 1; }
REPO="andresgarcia29/harness-daemon"

sha() { awk -v f="harnessd-$1" '$2==f {print $1}' "$SUMS"; }
for p in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  [ -n "$(sha "$p")" ] || { echo "❌ falta harnessd-$p en SHA256SUMS"; exit 1; }
done

mkdir -p "$DIR/dist"
cat > "$DIR/dist/harness.rb" <<EOF
# Formula generada por scripts/gen-formula.sh — no editar a mano.
class Harness < Formula
  desc "Harness de ingeniería agéntica multi-repo: wizard de init, panel local y generador determinista"
  homepage "https://github.com/${REPO}"
  version "${VER}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/${REPO}/releases/download/v${VER}/harnessd-darwin-arm64"
      sha256 "$(sha darwin-arm64)"
    else
      url "https://github.com/${REPO}/releases/download/v${VER}/harnessd-darwin-amd64"
      sha256 "$(sha darwin-amd64)"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/${REPO}/releases/download/v${VER}/harnessd-linux-arm64"
      sha256 "$(sha linux-arm64)"
    else
      url "https://github.com/${REPO}/releases/download/v${VER}/harnessd-linux-amd64"
      sha256 "$(sha linux-amd64)"
    end
  end

  def install
    binary = Dir["harnessd-*"].first || "harness"
    bin.install binary => "harness"
    bin.install_symlink "harness" => "harnessd"
  end

  test do
    assert_match "${VER}", shell_output("#{bin}/harness version")
  end
end
EOF
echo "✅ dist/harness.rb (v${VER})"
