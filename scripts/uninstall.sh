#!/bin/sh
# Stops the background service and removes PowerPilot.app. Settings and logs are kept.
set -eu

app="$HOME/Applications/PowerPilot.app"
if [ -x "$app/Contents/MacOS/powerpilot" ]; then
	"$app/Contents/MacOS/powerpilot" uninstall
fi
rm -rf "$app"
