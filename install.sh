#!/usr/bin/env sh
# install.sh — download and install straddler to ~/.local/bin (no sudo required)
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/CurtMeadows/straddler/main/install.sh | sh
#
# Override the install directory:
#   INSTALL_DIR=~/bin curl -sSL ... | sh

set -e

REPO="CurtMeadows/straddler"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ── Detect OS ────────────────────────────────────────────────────────────────

OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux"  ;;
  Darwin) OS="darwin" ;;
  *)
    echo "error: unsupported OS: $OS" >&2
    echo "       Download a binary manually from https://github.com/$REPO/releases" >&2
    exit 1
    ;;
esac

# ── Detect architecture ──────────────────────────────────────────────────────

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64)          ARCH="amd64" ;;
  aarch64 | arm64 | armv8) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# ── Resolve latest version ───────────────────────────────────────────────────

echo "=> Fetching latest straddler release..."

if command -v curl >/dev/null 2>&1; then
  VERSION="$(curl -sSfL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
elif command -v wget >/dev/null 2>&1; then
  VERSION="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
else
  echo "error: curl or wget is required" >&2
  exit 1
fi

if [ -z "$VERSION" ]; then
  echo "error: could not determine latest release version" >&2
  exit 1
fi

echo "   version: $VERSION"
echo "   platform: ${OS}/${ARCH}"

# ── Download and extract ─────────────────────────────────────────────────────

FILENAME="straddler_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"
TMP="$(mktemp -d)"

trap 'rm -rf "$TMP"' EXIT

echo "=> Downloading $FILENAME..."

if command -v curl >/dev/null 2>&1; then
  curl -sSfL "$URL" | tar -xz -C "$TMP"
else
  wget -qO- "$URL" | tar -xz -C "$TMP"
fi

# ── Install ──────────────────────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR"
mv "$TMP/straddler" "$INSTALL_DIR/straddler"
chmod +x "$INSTALL_DIR/straddler"

echo "=> Installed straddler $VERSION to $INSTALL_DIR/straddler"

# ── PATH check ───────────────────────────────────────────────────────────────

PATH_LINE="export PATH=\"\$PATH:$INSTALL_DIR\""

path_is_set() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return 0 ;;
    *) return 1 ;;
  esac
}

add_to_profile() {
  PROFILE="$1"
  if [ -f "$PROFILE" ] && ! grep -qF "$INSTALL_DIR" "$PROFILE" 2>/dev/null; then
    printf '\n# Added by straddler installer\n%s\n' "$PATH_LINE" >> "$PROFILE"
    echo "   added to $PROFILE"
  fi
}

if ! path_is_set; then
  echo "=> Adding $INSTALL_DIR to PATH..."
  add_to_profile "$HOME/.zshrc"
  add_to_profile "$HOME/.bashrc"
  add_to_profile "$HOME/.profile"
  echo ""
  echo "   Restart your shell or run:"
  echo "   export PATH=\"\$PATH:$INSTALL_DIR\""
fi

echo ""
echo "=> Done! Run 'straddler version' to confirm."
