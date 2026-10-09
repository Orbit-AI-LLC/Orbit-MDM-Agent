//go:build darwin

package system

import (
	"context"
	"os"
	"os/exec"
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
