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
