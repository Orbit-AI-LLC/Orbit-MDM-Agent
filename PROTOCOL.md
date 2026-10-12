# Orbit RMM agent wire protocol (normative)

This is the source of truth for the wire contract between the **agent** (Go for
Windows/Linux in `cmd/`+`internal/`; Swift for macOS in `macos-agent/`) and the
**server** (`../Orbit MDM`, `apps/rmm`). Every agent implementation must conform
to this exactly. When the wire changes: update this file, regenerate the golden
vectors (`go run ./scripts/gen_vectors`), and update **all three** sides (Go,
Swift, Python) plus their tests, then bump the version markers (`/v1/`, pulse
`"orbit-pulse/1"`).

The JSON on the wire is **order-insensitive** (the server uses `json.Unmarshal`
into structs; the menu-bar app and Swift agent use `JSONDecoder`), so key order is
not part of the contract. Go `,omitempty` fields map to Swift `Optional` encoded
with `encodeIfPresent`. The two places that **are** byte-exact — task
verify-then-parse and the pulse AEAD — operate on raw bytes, never on re-encoded
JSON (see below).

## Golden vectors
`testdata/vectors/` holds frozen vectors generated from the Go implementation and
consumed by Go tests (`internal/tasks/vectors_test.go`), Swift tests
(`macos-agent/Tests/OrbitAgentKitTests/`), and — in time — the server's tests.
Matching the vectors is defined as matching the Go reference.

## 1. HTTP API — `<server>/api/rmm/v1/`
JSON over HTTPS. Headers on every request: `Content-Type: application/json`,
`Accept: application/json`, `User-Agent: orbit-agent/<version>`. Every request
**except `enroll`** sends `Authorization: Bearer <secret>` (the agent secret,
`orba_…`; the server stores only its SHA-256).

**No global HTTP timeout.** Per-request deadlines: `checkin` 60s, `wait` 90s,
`tasks/<id>/result` 120s, `enroll` 60s, `pulse_key`/`start` 30s. A session-wide
cap would cut the 90s long poll short (on Swift, `URLSession`'s default 60s
request timeout must be overridden per request).

Status codes: `410`→retired (agent self-removes), `401`→unauthorized, other
`>=400`→error with body `{"error":<code>,"message":<msg>}`.

| Method & path | Auth | Request | Response |
| --- | --- | --- | --- |
| `POST enroll` | no | `{token,hostname,os,os_version,arch,serial,machine_id,manufacturer,model,version}` | `201 {agent_id,secret,server_key,checkin_seconds,pulse_addr,device}` |
| `POST checkin` | yes | `{version,metrics:{…}}` | `{tasks:[sealed],checkin_seconds,live,watch_services:[…],pulse_addr,server_time,support?}` |
| `GET wait` | yes | — | `{tasks:[sealed],live}` (long poll) |
| `POST pulse_key` | yes | `{pulse_key:"<b64 32-byte Ed25519 pub>"}` | `200 {}` |
| `POST tasks/<id>/start` | yes | `{}` | `{}` |
| `POST tasks/<id>/result` | yes | `{exit_code:int|null,stdout,stderr,data?,error?}` | `{}` |

`metrics` carries `cpu`, `memory`, `uptime_hours` (1 dp), `disks:[{used_percent,
total_gb,free_gb}]`, `reboot_pending`, `reboot_pending_since`, `permissions{}`.
`server_key` is pinned at enrollment and never re-fetched (trust-on-first-use).

## 2. Signed tasks (verify-then-parse)
Primitive: **Ed25519**. The server signs the **exact envelope bytes** (compact
JSON, sorted keys — `apps/rmm/signing.py`). The agent must:

1. base64-std-decode `server_key` (32 bytes), `envelope`, `signature`.
2. `Ed25519.verify(serverKey, envelopeBytes, signature)` → reject `signature`.
3. **Parse the same `envelopeBytes`** (never re-encode before verifying).
4. reject `notForUs` if `agent != our id`; `expired` if `expires!=0 && now>expires`
   (absolute unix seconds; `0` = no expiry); `replayed` if the id is in the replay
   store.

Outer: `{id, envelope:"<b64 std>", signature:"<b64 std>"}`. Envelope body:
`{id, agent, kind, payload, issued, expires}`. The payload is re-parsed by the
handler, never re-verified, so its byte form is not a contract.

Replay store (`seen.json`, state dir): JSON map `{id: unix_seconds}`, pruned to
30 days, written atomically (temp+rename, 0600/0700). The id is **claimed before
the task runs** so it can never run twice.

## 3. Pulse channel (instant task pickup)
Raw TCP to `pulse_addr` (`host:port`); TLS-free (a proxy fronts it). Framing:
**2-byte big-endian length prefix + payload**, `MAX_FRAME` 4096, zero-length
rejected. Carries only keepalives and a `nudge` — never a task.

Handshake:
1. agent → `{"v":1,"agent":"<id>","cn":"<b64 32B nonce>","ce":"<b64 X25519 eph pub>"}`
2. server → `{"sn":"<b64 32B>","se":"<b64 X25519 eph>","sig":"<b64 ed25519 sig>"}`
3. transcript = `"orbit-pulse/1"` + `0x00` + `cn + ce + sn + se`; verify `sig` with
   the **pinned server key**.
4. agent → `{"sig":"<b64 ed25519 over transcript, signed with the agent's pulse key>"}`
5. X25519 ECDH → shared secret.

Session keys: **HKDF-SHA256**, IKM = shared secret, salt = `cn + sn`, two 32-byte
keys with info `"orbit-pulse/1 agent->server"` and `"orbit-pulse/1 server->agent"`.
Sealed frames: **ChaCha20-Poly1305**, nonce = 4 zero bytes + **big-endian uint64
counter per direction**, no AAD. Control (sealed JSON): `{"t":"ping"|"pong"|
"nudge"}`; `nudge` → immediate check-in. Timers: ping 15s, idle 40s, dial 20s,
handshake 15s. The server's first sealed frame confirms key agreement.

The agent's pulse key is its own Ed25519 keypair; the 32-byte seed is stored in
`config.json` (`pulse_seed`, base64 std) and the public half registered via
`POST pulse_key`.

## 4. Remote sessions (relay WebSocket)
Triggered by a signed task `kind:"remote"`, payload `{session,mode,shell,relay,by}`
(`mode` ∈ `terminal|desktop`). The agent dials `relay` (`ws`/`wss`) with
`Authorization: Bearer <secret>`. The relay's first frame is **text**
`{"type":"open","cols":<u16>,"rows":<u16>}`. Thereafter: **binary** frames = raw
stream bytes; **text** frames = control JSON. Agent inbound limit 1 MiB.

- Browser→agent control: only `{"type":"resize","cols","rows"}`.
- Agent→browser control: `{"type":"ready"}`, `{"type":"exit","code":<int>}`,
  `{"type":"error","message":<str>}`.
- **Terminal**: a login shell on a PTY; emit `ready`, pipe bytes, honor `resize`,
  emit `exit`. Allowed shells (macOS): `bash`, `zsh`, `sh` (first = default).
- **Desktop**: try a local VNC server on `127.0.0.1:5900/5901/5902`; else the
  built-in **RFB 3.8** server — security None; pixel format 32bpp, depth 24,
  little-endian, true-colour, shifts red 16 / green 8 / blue 0 (**BGRX**), name
  `"Orbit RMM"`; `FramebufferUpdateRequest` throttled to 250ms (~4fps); encodings
  Raw (0) and **Zlib (6)** (one zlib stream per session, level 6, flushed per
  frame, 4-byte length prefix). Capture `screencapture -x -C -t png -D 1`,
  downscale to max dimension 1440. **Input injection is a no-op (view-only).**
  macOS zlib must use system **libz** (`CZlib`), not `Compression.framework`,
  which cannot reproduce the stream framing.

## 5. Config file (`config.json`)
0600 in a 0700 dir; `/Library/Application Support/Orbit Agent/` on macOS;
`ORBIT_AGENT_HOME` overrides (same variable name across Go and Swift — the
side-by-side test mechanism). Fields: `server, agent_id, secret, server_key,
checkin_seconds, device, pulse_addr?, pulse_seed?, support?`. `checkin_seconds`
defaults to 60 when ≤0. The three secrets (`secret`, `server_key`, `pulse_seed`)
live only here.

## 6. Status file (`status.json`)
0644, world-readable, `/Library/Orbit/status.json` on macOS; read by the menu-bar
app. Schema (`internal/status/status.go`, frozen): `{version, hostname?, os,
enrolled, server?, device?, last_checkin?, last_checkin_ok, healthy, issues:[{id,
severity,title,detail?,fix?}], permissions?{full_disk_access,screen_recording,
accessibility: granted|denied|unknown}, support{name?,phone?,email?,url?},
updated_at}`. `issues` is always present (`[]` when none); `support` is always
present (may be `{}`).
