#!/bin/sh
# Build dist/Orbit-Agent-macOS.pkg from the darwin builds in dist
# (scripts/build_agent.sh first): one universal binary at
# /Library/Orbit/orbit-agent, where `orbit-agent install` keeps it, and a
# postinstall that runs `orbit-agent install --package` (README.md).
#
# Signed when these are set (the agent workflow sets them from secrets), and
# notarized and stapled when the Apple ID ones are set too:
#   MAC_APP_IDENTITY        "Developer ID Application: …": the darwin binaries
#   MAC_INSTALLER_IDENTITY  "Developer ID Installer: …": the package
#   KEYCHAIN                the keychain holding them (default: the search list)
#   NOTARY_APPLE_ID, NOTARY_PASSWORD, APPLE_TEAM_ID
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(cat VERSION)}"
dist=dist
pkg="$dist/Orbit-Agent-macOS.pkg"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

keychain() { if [ -n "${KEYCHAIN:-}" ]; then printf '%s\n' --keychain "$KEYCHAIN"; fi; }

# The thin binaries are what agents update themselves from, so they're signed
# too; lipo keeps each slice's signature.
for arch in arm64 amd64; do
  if [ -n "${MAC_APP_IDENTITY:-}" ]; then
    # shellcheck disable=SC2046
    codesign --force --options runtime --timestamp --identifier ai.orbit.agent $(keychain) --sign "$MAC_APP_IDENTITY" "$dist/orbit-agent-darwin-$arch"
  fi
done
mkdir -p "$work/root/Library/Orbit" "$work/scripts"
lipo -create -output "$work/root/Library/Orbit/orbit-agent" "$dist/orbit-agent-darwin-arm64" "$dist/orbit-agent-darwin-amd64"
chmod 755 "$work/root/Library/Orbit/orbit-agent"
if [ -n "${MAC_APP_IDENTITY:-}" ]; then
  codesign --verify --strict "$work/root/Library/Orbit/orbit-agent"
fi

# The menu-bar app (menubar/macos), universal, in Orbit Agent.app beside the
# binary, loaded as a per-user LaunchAgent so it runs in each GUI session.
swiftc="$(xcrun -f swiftc)"
sdk="$(xcrun --show-sdk-path)"
app="$work/root/Library/Orbit/Orbit Agent.app"
mkdir -p "$app/Contents/MacOS"
for arch in arm64 x86_64; do
  "$swiftc" -sdk "$sdk" -O -target "$arch-apple-macos12" -o "$work/menu-$arch" menubar/macos/OrbitAgentMenu.swift
done
lipo -create -output "$app/Contents/MacOS/OrbitAgentMenu" "$work/menu-arm64" "$work/menu-x86_64"
chmod 755 "$app/Contents/MacOS/OrbitAgentMenu"
# The same universal binary as a standalone release asset: agents that update
# only their binary download it to refresh the menu-bar app (internal/agent's
# refreshMenuApp, internal/service). The manifest step adds it to SHA256SUMS and
# releases.json.
cp "$app/Contents/MacOS/OrbitAgentMenu" "$dist/orbit-agent-menu-darwin"
if [ -n "${MAC_APP_IDENTITY:-}" ]; then
  # shellcheck disable=SC2046
  codesign --force --options runtime --timestamp --identifier ai.orbit.agent.menu $(keychain) --sign "$MAC_APP_IDENTITY" "$dist/orbit-agent-menu-darwin"
  codesign --verify --strict "$dist/orbit-agent-menu-darwin"
fi
sed "s/__VERSION__/$VERSION/g" menubar/macos/Info.plist > "$app/Contents/Info.plist"
if [ -n "${MAC_APP_IDENTITY:-}" ]; then
  # shellcheck disable=SC2046
  codesign --force --options runtime --timestamp --identifier ai.orbit.agent.menu $(keychain) --sign "$MAC_APP_IDENTITY" "$app"
  codesign --verify --strict "$app"
fi
mkdir -p "$work/root/Library/LaunchAgents"
cp packaging/macos/ai.orbit.agent.menu.plist "$work/root/Library/LaunchAgents/ai.orbit.agent.menu.plist"
chmod 644 "$work/root/Library/LaunchAgents/ai.orbit.agent.menu.plist"

cp packaging/macos/postinstall "$work/scripts/postinstall"
chmod 755 "$work/scripts/postinstall"
# No extended attributes: they'd go into the payload as ._ files.
xattr -cr "$work/root" "$work/scripts"

pkgbuild --root "$work/root" --identifier ai.orbit.agent --version "$VERSION" \
  --scripts "$work/scripts" --install-location / "$work/component.pkg"
# A product archive: what MDMs (and Apple's InstallEnterpriseApplication) take.
if [ -n "${MAC_INSTALLER_IDENTITY:-}" ]; then
  # shellcheck disable=SC2046
  productbuild --package "$work/component.pkg" $(keychain) --sign "$MAC_INSTALLER_IDENTITY" "$pkg"
else
  productbuild --package "$work/component.pkg" "$pkg"
  echo "Built $pkg unsigned (set MAC_INSTALLER_IDENTITY to sign it)."
fi

if [ -n "${MAC_INSTALLER_IDENTITY:-}" ] && [ -n "${NOTARY_APPLE_ID:-}" ]; then
  xcrun notarytool submit "$pkg" --apple-id "$NOTARY_APPLE_ID" --password "$NOTARY_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
  xcrun stapler staple "$pkg"
fi
echo "Built $pkg ($VERSION)."
