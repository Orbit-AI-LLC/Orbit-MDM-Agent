# The Orbit agent

Orbit RMM's agent for Windows, macOS and Linux: one static Go binary, run by
the system as a service, that reports a computer's health and inventory to
Orbit MDM and runs the tasks Orbit RMM sends it.

The server is the Orbit MDM repository (`Orbit-AI-LLC/Orbit-MDM`, the
`Orbit MDM` folder beside this one): its `apps/rmm` is the agent's API, the
signed tasks and the one-line installers, which download this repository's
releases.

## Install

From the portal's **Enroll → Orbit agent**, copy the command for the
platform. It downloads the build for the machine, checks its SHA-256 and runs:

```sh
orbit-agent install --server https://mdm.example.com --token orbe_…
```

which enrolls with the install token, keeps the agent's identity in its
configuration (readable by root or SYSTEM only) and installs the service:

| | Service | Binary | Configuration |
| --- | --- | --- | --- |
| Windows | `OrbitAgent` (automatic, restarted on failure) | `C:\Program Files\Orbit\Agent\orbit-agent.exe` | `C:\ProgramData\Orbit\Agent\config.json` |
| macOS | launch daemon `ai.orbit.agent` | `/Library/Orbit/orbit-agent` | `/Library/Application Support/Orbit Agent/config.json` |
| Linux | systemd unit `orbit-agent` | `/usr/local/bin/orbit-agent` | `/etc/orbit-agent/config.json`, state in `/var/lib/orbit-agent` |

`orbit-agent status`, `orbit-agent once` (check in and run what comes back)
and `orbit-agent uninstall` do what they say. `ORBIT_AGENT_HOME` moves the
configuration and state (for tests, and to run a second copy beside the
service); `install --no-service` enrolls without copying the binary or
installing the service.

### Through an MDM

Each release has packages for deploying the agent with an MDM (Intune, Jamf,
Kandji, Orbit MDM's own app deployment) or a group policy. Both put the binary
where `install` keeps it and run `orbit-agent install --package`: a computer
that is already enrolled keeps its enrollment and starts the new version (an
upgrade never enrolls again, so a single-use token isn't spent twice);
otherwise it enrolls with the server and install token given, and with none
it stays installed, unenrolled, and says so.

- **Windows:** `Orbit-Agent-Windows-x64.msi` or `-arm64.msi`, per machine:

  ```bat
  msiexec /i Orbit-Agent-Windows-x64.msi SERVER=https://mdm.example.com TOKEN=orbe_… /qn
  ```

  or set `Server` and `Token` (strings) under
  `HKLM\SOFTWARE\Policies\Orbit\Agent` and install it with no properties.
  The token is kept out of the install log. Uninstalling it runs
  `orbit-agent uninstall`; an upgrade stops the service, replaces the file and
  starts it again.
- **macOS:** `Orbit-Agent-macOS.pkg`, one universal binary. Give it the server
  and token with a configuration profile's custom settings for the
  `ai.orbit.agent` domain (keys `Server` and `Token`), installed before or with
  the package; the postinstall log is `/var/log/orbit-agent-install.log`.
  `scripts/package_agent_macos.sh` builds it from `dist`.

## What it does

Every check-in (default a minute) sends CPU, memory, every disk, uptime,
whether a restart is pending and the state of the services the organization
watches, and gets back tasks. While someone has the computer open in the
portal, it waits on `/api/rmm/v1/wait` between check-ins so a command runs at
once. Tasks run one after another on a worker, so a long patch install never
holds up the check-ins.

| Task | Windows | macOS | Linux |
| --- | --- | --- | --- |
| Script | PowerShell, Command Prompt, Python | zsh, bash, sh, Python | bash, sh, Python |
| Inventory | CIM, the registry's uninstall keys, BitLocker, firewall, Defender | `system_profiler`, `fdesetup`, the application firewall, Gatekeeper | DMI, `/proc`, dpkg or rpm, LUKS, ufw/firewalld/nftables |
| Patch scan / install | Windows Update's COM API, with MSRC severities | `softwareupdate` | apt (security pocket = important), dnf with `updateinfo`, zypper |
| Software install | `.msi` (`msiexec /qn`), `.exe` with its silent switch | `.pkg` (`installer`) | `.deb` (apt), `.rpm` (dnf or zypper) |
| Restart, shut down | `shutdown.exe`, a minute's notice | `shutdown`, a minute's notice | `shutdown`, a minute's notice |
| Lock | a one-off task in the signed-in person's session | `pmset displaysleepnow` | `loginctl lock-sessions` |
| Remote session | a terminal (ConPTY) or a VNC desktop | a terminal (PTY) or the built-in VNC desktop | a terminal (PTY) or x11vnc |

Scripts can run as the signed-in person instead of root on macOS
(`launchctl asuser`) and Linux (`runuser`); on Windows they run as SYSTEM.
Output is kept to 512 KB a stream, and a script that runs past its timeout is
stopped with everything it started.

## The menu-bar app (macOS)

A small menu-bar app (`menubar/macos/`, built with `swiftc`) shows whoever is at
a Mac how the agent is doing: healthy or not, the last check-in, any privacy
permissions it still needs (each with a button to the right System Settings
pane) and the organization's help desk. It holds no secrets and does nothing
privileged — it only reads the status file the service writes each check-in
(`/Library/Orbit/status.json`, `internal/status/`). It's an accessory
`Orbit Agent.app` in `/Library/Orbit`, run in each GUI session by a per-user
LaunchAgent (`ai.orbit.agent.menu`).

The `.pkg` lays the app down and the postinstall starts it. But an agent updates
itself by replacing only its binary — and the binary that runs an update is the
old one — so the service also writes the app itself. At startup, when the app is
missing or its version doesn't match (`internal/agent`'s `ensureMenuApp`), and
again right after a self-update (`refreshMenuApp`), it downloads
`orbit-agent-menu-darwin`, the universal menu binary published beside the agent
binary in the same release, checks it against that release's `SHA256SUMS`, writes
the bundle and LaunchAgent around it, and reloads it for whoever is signed in.
The startup check is a no-op, with no network, once the installed app matches, so
it costs nothing on a healthy computer. So a Mac enrolled before the app existed,
or that updated binary-only, gets the icon the next time the agent starts — no
reinstall, no waiting for another release.

## The pulse channel

So work doesn't wait for the next check-in, the agent keeps one connection open
to the server (`internal/pulse/`, the server's `apps/rmm/pulse.py`). The agent
always dials out to `pulse_addr` (from a check-in; a Railway TCP proxy in front
of the server's `rmm_pulse` service), so nothing listens on the computer, and
reconnects by itself whenever the link drops. When the server has a task it
sends a **nudge** and the agent checks in at once over HTTPS; the link carries
only that nudge and keepalives, never a task.

The link is raw TCP, so it is authenticated and encrypted here. The server
proves itself by signing the handshake with the Ed25519 key the agent pinned at
enrollment — a fake or intercepting server can't, and is dropped. The agent
proves itself by signing with its own Ed25519 key, generated on first use and
registered over the authenticated API (`POST pulse_key`). Both sign a fresh
X25519 exchange into the transcript, so a man in the middle can't substitute
keys and the keys are forward secret; every frame after is ChaCha20-Poly1305, a
separate key and a counter nonce each way.

## Remote sessions

A `remote` task (`internal/remote/`) brings the agent to a live session an
admin opened in the portal: it opens a WebSocket to the relay in Orbit MDM
(Orbit MDM's `apps/rmm/remote.py`), authenticated with its own secret, and passes bytes
between the far end and a local one. The session runs in the background, so
check-ins and other tasks go on as usual, and ends when the relay closes it.

- **Terminal:** a real pseudo-terminal running the computer's shell as
  SYSTEM or root — colour, full-screen programs, tab completion and resizing,
  not one command at a time. Windows uses ConPTY (`conhost`), macOS and Linux
  a PTY with a login shell.
- **Remote desktop:** the agent relays an RFB stream to the admin's browser
  (noVNC). It uses a VNC server already listening on the computer if there is
  one; otherwise it serves its **own, built in** (`internal/remote/vnc.go`),
  bound to an in-process pipe — nothing opens a port, so only the agent can
  reach it. The built-in server captures the screen (Zlib-compressed, scaled
  down so frames stay light) and needs no screen sharing turned on.
  - **macOS:** built in, using the system `screencapture`. It needs the Screen
    Recording permission, which an MDM grants the agent with a PPPC profile
    (System Settings → Privacy & Security → Screen Recording otherwise). The
    session is **view-only** for now; mouse and keyboard control is the next
    step (it needs the CoreGraphics event bridge).
  - **Windows:** uses a VNC server if one is installed (TightVNC, UltraVNC,
    TigerVNC); a built-in capture server is not implemented yet.
  - **Linux:** uses one if present, or starts `x11vnc` for the session signed
    in at the screen, bound to localhost.

  Whoever is at the computer is told their screen is being viewed.

## Why a task can be trusted

Each task arrives as base64 bytes and an Ed25519 signature over exactly those
bytes, made with the server's key. The agent pinned that key when it enrolled
and checks, before anything runs, that the signature is good, the task names
this agent, it hasn't expired, and it isn't one it has run before (task ids
are recorded before a task starts, in `seen.json`). Someone who can change the
traffic, or who has a copy of the server's database, still can't make a
computer run anything. Downloads (software, agent updates) are checked against
the SHA-256 in the signed task before they are opened.

## Building

```sh
go vet ./... && go test ./...
sh scripts/build_agent.sh       # every platform into dist, with SHA256SUMS and releases.json
```

`.github/workflows/agent.yml` builds and tests on every push and pull
request, builds the `.pkg` (on macOS) and the `.msi` files (on Windows, with
WiX), and **on every push to `main` tags and publishes a release**,
`agent-v<version>`. The version is the next patch after the newest tag, or
`VERSION` when it has been raised there (a major or minor release;
`scripts/next_version.sh`). Pushing a tag `agent-v<version>` by hand releases
that version instead. With its secrets set (listed at the top of the workflow)
the macOS binaries are signed with a Developer ID and the `.pkg` with a
Developer ID Installer certificate and notarized; the Windows binaries and
`.msi` files are Authenticode-signed. Without them everything is built
unsigned, with a warning. The macOS `.pkg` build also publishes
`orbit-agent-menu-darwin`, the universal menu-bar binary agents refresh the
menu-bar app from. `SHA256SUMS` (every published file) and `releases.json` (the
version and each agent binary's URL and SHA-256; not the menu binary, which
isn't a per-platform agent build) are written after signing
(`build_agent.sh manifest`).

Orbit MDM reads `releases.json` from the latest release on its own (cached for
ten minutes): its installers download that release's binaries and check them,
and agents running an older version are sent an `update_agent` task and replace
themselves. Nothing on the server needs changing for a release.

## Not yet

Running scripts as the signed-in person on Windows.
