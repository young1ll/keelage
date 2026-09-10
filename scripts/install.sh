#!/bin/sh
# keelage installer (implementation-plan-v0 §6.1):
#   curl -fsSL https://raw.githubusercontent.com/young1ll/keelage/main/scripts/install.sh | sh
# Downloads the latest release for this OS/arch, checks the sha256 from
# checksums.txt, verifies the keyless cosign bundle when cosign is on PATH
# (set KEELAGE_REQUIRE_COSIGN=1 to refuse installing without it), and puts
# `keelage` in $KEELAGE_INSTALL_DIR (default ~/.local/bin). Then: keelage setup
set -eu
REPO="${KEELAGE_REPO:-young1ll/keelage}"
VERSION="${KEELAGE_VERSION:-latest}"
DIR="${KEELAGE_INSTALL_DIR:-$HOME/.local/bin}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in linux|darwin) ;; *) echo "install.sh: unsupported OS $os (linux, darwin)" >&2; exit 1 ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "install.sh: unsupported arch $arch" >&2; exit 1 ;; esac
if [ "$VERSION" = latest ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$VERSION" ] || { echo "install.sh: could not resolve the latest release" >&2; exit 1; }
fi
ver="${VERSION#v}"
base="https://github.com/$REPO/releases/download/$VERSION"
archive="keelage_${ver}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "keelage $VERSION ($os/$arch) → $DIR"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
if command -v cosign >/dev/null 2>&1; then
  curl -fsSL "$base/checksums.txt.sigstore.json" -o "$tmp/checksums.txt.sigstore.json"
  cosign verify-blob "$tmp/checksums.txt" --bundle "$tmp/checksums.txt.sigstore.json" \
    --certificate-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$VERSION" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null
  echo "signature: verified (sigstore keyless, release.yml@$VERSION)"
elif [ "${KEELAGE_REQUIRE_COSIGN:-0}" = 1 ]; then
  echo "install.sh: cosign not found and KEELAGE_REQUIRE_COSIGN=1" >&2; exit 1
else
  echo "signature: cosign not on PATH, checksum only (install cosign to verify the release signature)"
fi
want="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$want" ] || { echo "install.sh: $archive not in checksums.txt" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"; else got="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"; fi
[ "$want" = "$got" ] || { echo "install.sh: checksum mismatch for $archive" >&2; exit 1; }
echo "checksum: ok"
tar -xzf "$tmp/$archive" -C "$tmp" keelage
mkdir -p "$DIR"
install -m 0755 "$tmp/keelage" "$DIR/keelage"
echo "installed $DIR/keelage"
case ":$PATH:" in *":$DIR:"*) ;; *) echo "add $DIR to PATH, then:" ;; esac
echo "next: keelage setup        # hooks + MCP for Claude Code (each item asks y/N)"
echo "      keelage daemon &      # then edit in Claude Code: constraints are injected before edits"
