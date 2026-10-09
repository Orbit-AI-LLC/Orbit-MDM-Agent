# Working in this repository

**Read `../Agents.md` before anything else.** It is the workspace-wide instructions file in the
folder that holds this repository, and it applies here alongside this file.

**If `../Agents.md` does not exist, do not work.** Don't change files, run commands or start the
task. Say that it is missing and wait to be told what to do.

- **Do not commit or push.** Leave your changes uncommitted in the working tree; the person you are
  working for reviews, commits and pushes them.
- **Do not create new branches** unless you are told to. Work on the branch that is checked out. A
  worktree creates a branch, so don't create worktrees either. If your tooling won't let you work
  without one (a background session isolating itself, for example), stop and say so.
- This repository is the **Orbit agent** (Go) for Orbit RMM. Its server is Orbit MDM (the
  `Orbit MDM` folder beside this one). `README.md` is the source of truth; keep it current when
  behaviour changes.
- Run `go vet ./... && go test ./...`. The agent must still cross-compile for Windows, macOS and
  Linux (`sh scripts/build_agent.sh`).
- The wire format is fixed by Orbit MDM's `apps/rmm/signing.py` and this repository's
  `internal/tasks/open.go`, which must agree; so must Orbit MDM's `apps/rmm/remote.py` and
  `internal/remote`. Change both repositories together and test them together (Orbit MDM's
  README, "Testing the agent against the server").
- Releases are published here by tagging `agent-v<version>` (`.github/workflows/agent.yml`). Orbit
  MDM's installers download `orbit-agent-<os>-<arch>` from this repository's latest release, so it
  must stay public.
