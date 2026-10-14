#!/bin/sh
# Builds PowerPilot.app from source into ~/Applications and starts its
# background service (a LaunchAgent that runs at login).
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$root" && go build -trimpath -ldflags "-X main.version=dev" -o "$work/powerpilot" ./cmd/powerpilot)
mkdir -p "$HOME/Applications"
"$root/scripts/package-macos.sh" "$work/powerpilot" dev "$HOME/Applications" >/dev/null
# Earlier builds were called "UPS Monitor"; the install step also removes their LaunchAgent.
rm -rf "$HOME/Applications/UPS Monitor.app"
"$HOME/Applications/PowerPilot.app/Contents/MacOS/powerpilot" install
