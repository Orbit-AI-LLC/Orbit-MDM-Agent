//go:build darwin

package system

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/status"
)

// tccDB is macOS's system privacy database. Reading it needs Full Disk Access,
// so opening it is itself the test for that permission, and once it opens we can
// read whether the agent was granted the other permissions it needs.
const tccDB = "/Library/Application Support/com.apple.TCC/TCC.db"

// agentTCCIdentifier is the agent's code-signing identifier (set by codesign
// --identifier in the packaging script); TCC records a signed program by it.
const agentTCCIdentifier = "ai.orbit.agent"

// permissions reports the macOS privacy permissions the agent needs: Full Disk
// Access (complete inventory, and the gateway to reading the rest), Screen
// Recording (the built-in remote desktop), and Accessibility (remote control,
// once it lands). Each is "granted", "denied" or "unknown".
func permissions(ctx context.Context) map[string]string {
	out := map[string]string{
		"full_disk_access": status.Unknown,
		"screen_recording": status.Unknown,
		"accessibility":    status.Unknown,
	}
	switch f, err := os.Open(tccDB); {
	case err == nil:
		f.Close()
		out["full_disk_access"] = status.Granted
	case os.IsPermission(err):
		out["full_disk_access"] = status.Denied
	}
	// Screen Recording and Accessibility live in the same system database, which
	// we can only read with Full Disk Access; without it we can't tell.
	if out["full_disk_access"] == status.Granted {
		out["screen_recording"] = tccAuth(ctx, "kTCCServiceScreenCapture")
		out["accessibility"] = tccAuth(ctx, "kTCCServiceAccessibility")
	}
	return out
}

// tccAuth reads whether the agent is allowed one TCC service from the system
// database, with the sqlite3 that ships with macOS. The access table's
// auth_value is 2 when allowed (0 denied, 1 limited) on Mojave and later.
func tccAuth(ctx context.Context, service string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	query := "SELECT auth_value FROM access WHERE service='" + service +
		"' AND client IN ('" + agentTCCIdentifier + "','" + binaryClient() + "') ORDER BY auth_value DESC LIMIT 1;"
	out, err := exec.CommandContext(ctx, "sqlite3", tccDB, query).Output()
	if err != nil {
		return status.Unknown
	}
	switch strings.TrimSpace(string(out)) {
	case "":
		// No row: the agent has never been granted it.
		return status.Denied
	case "2":
		return status.Granted
	default:
		return status.Denied
	}
}

// binaryClient is how TCC records an unsigned program: by its path.
func binaryClient() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return ""
}

// ConsoleUser is whoever is signed in at the Mac's screen, or "" when that's
// nobody (the login window, where /dev/console belongs to root). A prompt can
// only be shown when someone is signed in, and the agent re-asks for Screen
// Recording when the console user changes (a new login).
func ConsoleUser() string {
	out, err := exec.Command("stat", "-f%Su", "/dev/console").Output()
	if err != nil {
		return ""
	}
	user := strings.TrimSpace(string(out))
	if user == "root" {
		return ""
	}
	return user
}

// RequestPermissions nudges macOS to ask the signed-in person for every privacy
// permission the agent needs but doesn't yet have. Not all of them can be asked
// for the same way:
//
//   - Screen Recording (the remote desktop) HAS a system prompt, but macOS only
//     raises it when a process actually attempts a capture while someone is signed
//     in, and only once per TCC decision — so merely reading the status never
//     asks. The agent takes a throwaway capture, which both lists it under Screen
//     Recording and raises the one-time prompt whenever the decision is still open,
//     including right after a self-update gave an unsigned build a fresh binary.
//   - Accessibility (remote keyboard and mouse) also has a system prompt, but the
//     agent doesn't use it yet (the remote desktop is view-only), so it isn't
//     requested until the feature that needs it lands — asking for a permission the
//     agent won't use would only confuse the person.
//   - Full Disk Access (complete inventory) has NO prompt at all: macOS never asks
//     for it, a program just silently can't read protected paths. There's nothing
//     to trigger here; the menu-bar app takes the person to the right System
//     Settings pane instead (requestMissingPermissions there), and an MDM can grant
//     it silently with the PPPC profile.
//
// It never disturbs a permission already granted, and is a silent no-op once the
// person has explicitly denied one. The agent calls it at startup (so a
// self-update's restart re-asks), and when it notices a login or a wake from sleep.
func RequestPermissions(ctx context.Context) {
	// No one signed in: there's no one to show a prompt to.
	if ConsoleUser() == "" {
		return
	}
	perms := permissions(ctx)
	// Screen Recording: raise the prompt by attempting a capture, unless we can
	// confirm it's already granted (a capture while granted is harmless anyway).
	if perms["screen_recording"] != status.Granted {
		requestScreenRecording(ctx)
	}
}

// requestScreenRecording takes a throwaway screen capture to raise macOS's Screen
// Recording prompt (see RequestPermissions).
func requestScreenRecording(ctx context.Context) {
	dir, err := os.MkdirTemp("", "orbit-perm-")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// -x no sound, -t png, -D 1 the main display, to a file we throw away.
	_ = exec.CommandContext(ctx, "screencapture", "-x", "-t", "png", "-D", "1", filepath.Join(dir, "probe.png")).Run()
}
