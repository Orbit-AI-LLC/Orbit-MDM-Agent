#!/bin/sh
# Builds the Swift macOS agent into per-architecture thin binaries and a universal
# binary, mirroring the menu-bar app's per-arch + lipo precedent but via SwiftPM.
#
# Outputs (default into macos-agent/.dist, kept apart from the Go build's dist/
# until the release cutover):
#   orbit-agent-darwin-arm64   thin arm64  (what the curl installer downloads)
#   orbit-agent-darwin-amd64   thin x86_64
#   orbit-agent                universal (arm64 + x86_64), for the .app bundle/.pkg
#
# During the migration these are a NON-PUBLISHED CI artifact; the Go darwin binary
# still ships. At cutover the macos CI job renames/relocates these into dist/ under
# the exact names the packaging script expects (see the migration plan). VERSION is
# injected so `orbit-agent version` / CFBundleShortVersionString match the release.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
pkg="$here/macos-agent"
out="${OUT:-$pkg/.dist}"
VERSION="${VERSION:-$(cat "$here/VERSION")}"

rm -rf "$out"
mkdir -p "$out"

# Swift arch name -> Go/release asset arch name. The version reaches the binary at
# the packaging stage (ServiceInfo.plist's __VERSION__, which `version` reads back
# from the bundle); the per-arch build itself needs no version injection.
slice() {
    sw="$1"; go="$2"
    echo "building $sw ($go) ..."
    swift build --package-path "$pkg" -c release --arch "$sw" >/dev/null
    bindir=$(swift build --package-path "$pkg" -c release --arch "$sw" --show-bin-path)
    cp "$bindir/orbit-agent" "$out/orbit-agent-darwin-$go"
}

slice arm64 arm64
slice x86_64 amd64

lipo -create -output "$out/orbit-agent" \
    "$out/orbit-agent-darwin-arm64" "$out/orbit-agent-darwin-amd64"

echo "built orbit-agent $VERSION into $out:"
ls -l "$out"
lipo -info "$out/orbit-agent"
