#!/usr/bin/env bash
set -euo pipefail

arch=${1:?usage: package-linux.sh <amd64|arm64> [version] [binary]}
version=${2:-dev}
binary=${3:-bin/client/mole-client-linux-$arch}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [[ "$binary" = /* ]]; then binary_path="$binary"; else binary_path="$root/$binary"; fi
output="$root/dist"
name="mole-client-linux-$arch"
stage="$output/$name"

rm -rf "$stage"
mkdir -p "$stage"
install -m 0755 "$binary_path" "$stage/mole-client"
install -m 0755 "$root/packaging/linux/install.sh" "$stage/install.sh"
cat > "$stage/README.txt" <<EOF
Mole Client $version for Linux $arch

Install:
  ./install.sh

Start:
  sudo mole-client -server SERVER_IP:8080 -relay-port 18081

Use list, connect, disconnect, status and exit in the interactive terminal.
EOF
tar -C "$output" -czf "$output/$name.tar.gz" "$name"
rm -rf "$stage"
echo "$output/$name.tar.gz"
