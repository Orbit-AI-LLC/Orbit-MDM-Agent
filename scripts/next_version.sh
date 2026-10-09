#!/bin/sh
# The version the next release gets: VERSION when it's newer than every
# agent-v<version> tag (raise it there for a major or minor release), else the
# newest tag's patch plus one. The release workflow tags it.
set -eu
cd "$(dirname "$0")/.."
floor="$(tr -d ' \n' < VERSION)"
by_version() { sort -t. -k1,1n -k2,2n -k3,3n; }
latest="$(git tag -l 'agent-v*' | sed 's/^agent-v//' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | by_version | tail -1 || true)"
if [ -z "$latest" ]; then echo "$floor"; exit 0; fi
if [ "$floor" != "$latest" ] && [ "$(printf '%s\n%s\n' "$floor" "$latest" | by_version | tail -1)" = "$floor" ]; then
  echo "$floor"
else
  echo "$latest" | awk -F. '{ print $1 "." $2 "." $3 + 1 }'
fi
