#!/bin/sh
# Render the Orbit RMM mark into the macOS app icon the agent ships.
#
# The agent's two bundles show this icon in System Settings and the Finder: the
# root service's "Orbit Agent.app" (what Full Disk Access, Screen Recording and
# Accessibility list) and the menu-bar "Orbit Agent.app" (a Login Item). The
# mark's geometry and colours live in scripts/orbitmark.swift, the family
# renderer every Orbit app shares (Orbit RMM's mark is a monitor with a
# heartbeat, in orbit, on magenta). This compiles it, renders the macOS icon
# grid at every size, and writes internal/service/OrbitAgent.icns, which
# menu_darwin.go embeds (so a self-updating agent keeps the icon without a
# download) and scripts/package_agent_macos.sh copies into each bundle's
# Resources.
#
# Build (the toolchain compiler is enough, no Xcode licence needed):
#
#   sh scripts/build_icon.sh
#
# Re-run after editing the mark; never hand-edit the output.
set -eu
cd "$(dirname "$0")/.."
mark=rmm
out=internal/service/OrbitAgent.icns
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

swiftc="$(xcrun -f swiftc)"
sdk="$(xcrun --show-sdk-path)"
"$swiftc" -sdk "$sdk" -O -o "$work/orbitmark" scripts/orbitmark.swift

set="$work/OrbitAgent.iconset"
mkdir -p "$set"
# The macOS icon grid: each entry is a point size at a scale, so a name maps to
# a pixel size (16@2x and 32@1x are both 32 px, and so on).
render() { "$work/orbitmark" png "$mark" "$set/$2" "$1" mac; }
render 16   icon_16x16.png
render 32   icon_16x16@2x.png
render 32   icon_32x32.png
render 64   icon_32x32@2x.png
render 128  icon_128x128.png
render 256  icon_128x128@2x.png
render 256  icon_256x256.png
render 512  icon_256x256@2x.png
render 512  icon_512x512.png
render 1024 icon_512x512@2x.png
iconutil -c icns "$set" -o "$out"
echo "Wrote $out ($(wc -c < "$out") bytes)."
