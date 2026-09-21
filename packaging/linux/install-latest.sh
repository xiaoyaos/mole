#!/bin/sh
set -eu

REPOSITORY="${MOLE_REPOSITORY:-xiaoyaos/mole}"
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported Linux architecture: $(uname -m)" >&2; exit 1 ;;
esac

ASSET="mole-client-linux-$ARCH.tar.gz"
BASE_URL="https://github.com/$REPOSITORY/releases/latest/download"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

curl -fsSL "$BASE_URL/$ASSET" -o "$TEMP_DIR/$ASSET"
curl -fsSL "$BASE_URL/SHA256SUMS" -o "$TEMP_DIR/SHA256SUMS"
EXPECTED=$(awk -v file="$ASSET" '$2 == file || $2 == "*" file { print $1; exit }' "$TEMP_DIR/SHA256SUMS")
[ -n "$EXPECTED" ] || { echo "SHA256SUMS does not contain $ASSET." >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TEMP_DIR/$ASSET" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$TEMP_DIR/$ASSET" | awk '{print $1}')
fi
[ "$EXPECTED" = "$ACTUAL" ] || { echo "Checksum verification failed." >&2; exit 1; }

tar -xzf "$TEMP_DIR/$ASSET" -C "$TEMP_DIR"
sh "$TEMP_DIR/mole-client-linux-$ARCH/install.sh"
