#!/usr/bin/env bash
# Installs the latest vanityrig release binary for your platform.
# Usage: curl -fsSL https://raw.githubusercontent.com/bytestrix/vanityrig/main/install.sh | bash
set -euo pipefail

REPO="bytestrix/vanityrig"
BIN_DIR="${VANITYRIG_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)

case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

case "$os" in
  linux|darwin) ;;
  *)
    echo "error: this script supports Linux and macOS only." >&2
    echo "Windows: download a .zip from https://github.com/$REPO/releases/latest" >&2
    exit 1
    ;;
esac

version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$version" ]; then
  echo "error: could not determine the latest release of $REPO" >&2
  exit 1
fi

archive="vanityrig_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$version/$archive"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading vanityrig $version for $os/$arch..."
curl -fsSL "$url" -o "$tmp/$archive"
tar -xzf "$tmp/$archive" -C "$tmp"

mkdir -p "$BIN_DIR"
mv "$tmp/vanityrig" "$BIN_DIR/vanityrig"
chmod +x "$BIN_DIR/vanityrig"

echo "Installed vanityrig $version to $BIN_DIR/vanityrig"
echo

case ":$PATH:" in
  *":$BIN_DIR:"*)
    echo "Run it: vanityrig"
    ;;
  *)
    echo "$BIN_DIR isn't on your PATH yet. Add it once:"
    echo
    echo "  echo 'export PATH=\"\$PATH:$BIN_DIR\"' >> ~/.zshrc   # or ~/.bashrc"
    echo "  source ~/.zshrc"
    echo
    echo "Then run: vanityrig"
    ;;
esac
