#!/bin/sh
set -eu

SOURCE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE="$SOURCE_DIR/mole-client"
DESTINATION="${MOLE_INSTALL_DIR:-/usr/local/bin}/mole-client"

if [ ! -x "$SOURCE" ]; then
  echo "mole-client is missing from this package." >&2
  exit 1
fi

if [ -w "$(dirname "$DESTINATION")" ]; then
  mkdir -p "$(dirname "$DESTINATION")"
  install -m 0755 "$SOURCE" "$DESTINATION"
else
  sudo sh -c 'mkdir -p "$(dirname "$1")" && install -m 0755 "$2" "$1"' sh "$DESTINATION" "$SOURCE"
fi

echo "Installed Mole client to $DESTINATION"
echo "Run: sudo mole-client -server SERVER_IP:8080 -relay-port 18081"
