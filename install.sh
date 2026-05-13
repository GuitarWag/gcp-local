#!/usr/bin/env sh
# install.sh — fetch the latest gcp-local release for the current OS+arch and
# drop the binary on PATH. POSIX sh; no bash-isms; works on macOS and Linux.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/GuitarWag/gcp-local/main/install.sh | sh
#   curl -sSL https://raw.githubusercontent.com/GuitarWag/gcp-local/main/install.sh | sh -s -- --version v0.2.0
#   curl -sSL https://raw.githubusercontent.com/GuitarWag/gcp-local/main/install.sh | sh -s -- --bin-dir ~/.local/bin
set -eu

REPO="GuitarWag/gcp-local"
VERSION="latest"
BIN_DIR=""
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    -h|--help)
      cat <<EOF
gcp-local installer.

Options:
  --version <tag>   release tag to install (default: latest)
  --bin-dir <path>  where to drop the binary (default: /usr/local/bin if writable, else ~/.local/bin)
EOF
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (use go install instead)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $arch (use go install instead)" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' \
    | head -n1 \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
fi
if [ -z "$VERSION" ]; then
  echo "could not resolve latest release tag" >&2
  exit 1
fi

# Tag is "v0.2.0", archive uses "0.2.0".
trimmed=$(printf '%s' "$VERSION" | sed -E 's/^v//')
archive="gcp-local_${trimmed}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$VERSION/$archive"

echo "→ downloading $url"
if ! curl -fsSL -o "$TMP_DIR/$archive" "$url"; then
  echo "download failed. Check that $VERSION has a release with $archive attached." >&2
  exit 1
fi

tar -xzf "$TMP_DIR/$archive" -C "$TMP_DIR"
binary="$TMP_DIR/gcp-local"
if [ ! -x "$binary" ]; then
  echo "expected binary not found in archive" >&2
  exit 1
fi

if [ -z "$BIN_DIR" ]; then
  if [ -w /usr/local/bin ]; then
    BIN_DIR="/usr/local/bin"
  else
    BIN_DIR="$HOME/.local/bin"
  fi
fi
mkdir -p "$BIN_DIR"
install -m 0755 "$binary" "$BIN_DIR/gcp-local"

echo "✓ gcp-local $VERSION installed to $BIN_DIR/gcp-local"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    echo
    echo "⚠ $BIN_DIR is not on your PATH. Add this to your shell rc:"
    echo "    export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

"$BIN_DIR/gcp-local" version 2>/dev/null || true
