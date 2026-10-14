#!/usr/bin/env bash
# Wraps a macOS powerpilot binary into PowerPilot.app (ad-hoc signed).
# usage: scripts/package-macos.sh <binary> <version> <outdir>
set -euo pipefail

bin=$1
version=${2#v}
out=$3
root=$(cd "$(dirname "$0")/.." && pwd)
[ "$version" = dev ] && version=0.0.0

app="$out/PowerPilot.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/powerpilot"
sed "s/__VERSION__/$version/g" "$root/packaging/Info.plist" >"$app/Contents/Info.plist"
cp "$root/packaging/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
codesign --force --sign - "$app"
echo "$app"
