#!/usr/bin/env bash
# Builds PowerPilot release archives into dist/.
# usage: scripts/build-release.sh <darwin|linux|windows> <version>
set -euo pipefail

os=$1
version=$2
root=$(cd "$(dirname "$0")/.." && pwd)
dist="$root/dist"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$dist"
cd "$root"

# build <goos> <goarch> <cgo> <output> [extra ldflags]
build() {
	GOOS=$1 GOARCH=$2 CGO_ENABLED=$3 go build -trimpath \
		-ldflags "-s -w -X main.version=$version ${5:-}" -o "$4" ./cmd/powerpilot
}

case $os in
darwin)
	CC=clang build darwin arm64 1 "$work/powerpilot-arm64"
	CC="clang -arch x86_64" build darwin amd64 1 "$work/powerpilot-amd64"
	lipo -create -output "$work/powerpilot" "$work/powerpilot-arm64" "$work/powerpilot-amd64"
	mkdir -p "$work/out"
	scripts/package-macos.sh "$work/powerpilot" "$version" "$work/out" >/dev/null
	(cd "$work/out" && ditto -c -k --keepParent PowerPilot.app "$dist/PowerPilot_${version}_macOS_universal.zip")
	;;
linux)
	for arch in amd64 arm64; do
		name="PowerPilot_${version}_linux_${arch}"
		mkdir -p "$work/$name"
		build linux "$arch" 0 "$work/$name/powerpilot"
		cp packaging/linux/70-powerpilot.rules packaging/linux/INSTALL.txt "$work/$name/"
		tar -C "$work" -czf "$dist/$name.tar.gz" "$name"
	done
	;;
windows)
	for arch in amd64 arm64; do
		name="PowerPilot_${version}_windows_${arch}"
		mkdir -p "$work/$name"
		# A GUI-subsystem exe: no console window on double-click or at sign-in.
		build windows "$arch" 0 "$work/$name/PowerPilot.exe" "-H=windowsgui"
		cp packaging/windows/README.txt "$work/$name/"
		(cd "$work" && zip -qr "$dist/$name.zip" "$name")
	done
	;;
*)
	echo "unknown os: $os (want darwin, linux or windows)" >&2
	exit 2
	;;
esac
ls -l "$dist"
