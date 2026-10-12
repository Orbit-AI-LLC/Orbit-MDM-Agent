# Orbit RMM agent — macOS (Swift)

The macOS agent, being ported from Go to Swift. Windows and Linux stay on the Go
binary in this repository (`../cmd`, `../internal`); this package builds only the
`darwin` agent. It must keep byte-for-byte wire parity with the Go agent and the
Python server — see [`../PROTOCOL.md`](../PROTOCOL.md) and the shared golden
vectors in [`../testdata/vectors`](../testdata/vectors).

Status: **in progress.** The Go `darwin` binary is still what ships. This Swift
binary is built in CI as a non-published artifact until it reaches full parity;
only then does the release manifest switch to it (one-line, reversible cutover).
See [`../.claude/plans`](../) / the migration plan for the staged roadmap.

## What's implemented so far
- `Support/` — paths + `ORBIT_AGENT_HOME`, `config.json` (0600, atomic),
  `status.json` (0644, schema frozen for the menu-bar app), the pulse Ed25519 key.
- `Crypto/` — Ed25519 **verify-then-parse** task verification (CryptoKit) and the
  `seen.json` replay store.
- `Wire/` — the API DTOs and an `HTTPClient` with per-request deadlines and no
  global timeout.
- `Sources/orbit-agent` — the CLI; `version` and `status` work. `install`, `run`,
  `once`, `uninstall` are stubbed (later stages).

Not yet ported: the check-in loop, system inventory/metrics/patches/TCC, service
management, self-update, the pulse channel, and remote terminal/VNC.

## Build
```sh
swift build -c release            # arm64 host; universal via the script below
```
`CZlib` links the system `libz` (for the RFB/VNC zlib stream). Minimum macOS 12.

Universal binary (what the .pkg/install path expects, matching the menu-bar app's
per-arch + lipo precedent):
```sh
sh ../scripts/build_swift_agent.sh     # -> macos-agent/.dist/orbit-agent-darwin-{arm64,amd64} + universal
```
The **x86_64 slice needs the full Xcode toolchain** (the CommandLineTools on
recent macOS lack the Swift x86_64 compatibility libraries), so on an arm64 dev
machine run the universal build under Xcode:
```sh
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer sh ../scripts/build_swift_agent.sh
```
`otool -L` on the result shows only OS-shipped libraries (`/usr/lib`,
`/System/Library`) — no embedded Swift runtime — so it signs/notarizes exactly
like the Go binary.

## Test
The tests use **XCTest**, which ships with full Xcode (not the CommandLineTools).
If `xcode-select -p` points at CommandLineTools, run with Xcode's toolchain
without switching globally:
```sh
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer swift test
```
CI's `macos-latest` runners have Xcode selected by default.

Regenerate the golden vectors after any wire change:
```sh
cd .. && go run ./scripts/gen_vectors
```
