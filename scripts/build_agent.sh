#!/bin/sh
# Build the Orbit agent for every platform into dist, and print the
# ORBIT_RMM_AGENT_RELEASES value for those builds (set the URL base first:
# BASE=https://github.com/Orbit-AI-LLC/Orbit-MDM-Agent/releases/download/agent-v1.0.0).
#
#   sh scripts/build_agent.sh            build, then write the manifest
#   sh scripts/build_agent.sh manifest   only the manifest, over what dist
#                                        has (after the binaries were signed)
#
# The manifest is SHA256SUMS (the binaries and any .pkg or .msi) and
# releases.json (the version and the binaries, which the server reads from
# the latest release for its installers and agents' updates).
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(cat VERSION)}"
BASE="${BASE:-https://github.com/Orbit-AI-LLC/Orbit-MDM-Agent/releases/latest/download}"
TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

name_for() {
  os="${1%/*}"; arch="${1#*/}"
  name="orbit-agent-$os-$arch"; [ "$os" = windows ] && name="$name.exe"
  echo "$name"
}

if [ "${1:-}" != manifest ]; then
  rm -rf dist && mkdir -p dist
  for target in $TARGETS; do
    CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "dist/$(name_for "$target")" ./cmd/orbit-agent
  done
fi

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
rm -f dist/SHA256SUMS
json="{\"version\": \"$VERSION\""
sep=", "
for target in $TARGETS; do
  name="$(name_for "$target")"
  sum="$(sha "dist/$name")"
  echo "$sum  $name" >> dist/SHA256SUMS
  json="$json$sep\"${target%/*}-${target#*/}\": {\"url\": \"$BASE/$name\", \"sha256\": \"$sum\"}"
  sep=", "
done
for package in dist/*.pkg dist/*.msi; do
  [ -f "$package" ] && echo "$(sha "$package")  ${package#dist/}" >> dist/SHA256SUMS
done
echo "$json}" > dist/releases.json
echo "Built orbit-agent $VERSION into dist."
echo "ORBIT_RMM_AGENT_VERSION=$VERSION"
echo "ORBIT_RMM_AGENT_RELEASES=$(cat dist/releases.json)"
