#!/usr/bin/env bash
set -euo pipefail

version=${1:-dev}
binary=${2:-bin/client/mole-client-darwin-arm64}
bundle_version=${version#v}
if [[ ! "$bundle_version" =~ ^[0-9]+([.][0-9]+){0,2}$ ]]; then
  bundle_version=0.0.0
fi
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [[ "$binary" = /* ]]; then binary_path="$binary"; else binary_path="$root/$binary"; fi
output="$root/dist"
stage="$output/macos"
app="$stage/Mole.app"

rm -rf "$stage"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$output"
swiftc -parse-as-library "$root/packaging/macos/MoleLauncher.swift" -framework Cocoa \
  -o "$app/Contents/MacOS/MoleLauncher"
install -m 0755 "$binary_path" "$app/Contents/Resources/mole-client"
cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>MoleLauncher</string>
<key>CFBundleIdentifier</key><string>com.xiaoyaos.mole.client</string>
<key>CFBundleName</key><string>Mole</string>
<key>CFBundleDisplayName</key><string>Mole</string>
<key>CFBundleShortVersionString</key><string>$bundle_version</string>
<key>CFBundleVersion</key><string>$bundle_version</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSAppleEventsUsageDescription</key><string>Mole opens Terminal so you can use its interactive client.</string>
</dict></plist>
EOF
codesign --force --deep --sign - "$app"
rm -f "$output/mole-client-macos-arm64.dmg"
hdiutil create -quiet -volname "Mole $version" -srcfolder "$stage" -ov -format UDZO \
  "$output/mole-client-macos-arm64.dmg"
rm -rf "$stage"
echo "$output/mole-client-macos-arm64.dmg"
