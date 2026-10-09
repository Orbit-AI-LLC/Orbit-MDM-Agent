//go:build darwin

package system

import (
	"context"
	"os/exec"
	"strings"
)

// ScanUpdates lists what Software Update offers.
func ScanUpdates(ctx context.Context) ([]Update, error) {
	out, err := exec.CommandContext(ctx, "softwareupdate", "--list", "--all").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "Software Update Tool") {
		return nil, err
	}
	return SoftwareUpdates(string(out)), nil
}

// InstallUpdates installs the named updates (all recommended when ids is empty).
// A macOS upgrade on Apple silicon needs the bootstrap token Orbit MDM escrows;
// the MDM's ScheduleOSUpdate is the dependable way to install those.
func InstallUpdates(ctx context.Context, ids []string) ([]map[string]any, string, error) {
	var log strings.Builder
	var results []map[string]any
	if len(ids) == 0 {
		out, err := exec.CommandContext(ctx, "softwareupdate", "--install", "--recommended", "--agree-to-license").CombinedOutput()
		return nil, string(out), err
	}
	var firstErr error
	for _, id := range ids {
		out, err := exec.CommandContext(ctx, "softwareupdate", "--install", id, "--agree-to-license").CombinedOutput()
		log.Write(out)
		results = append(results, map[string]any{"id": id, "ok": err == nil, "error": errText(err, out)})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return results, log.String(), firstErr
}

func errText(err error, out []byte) string {
	if err == nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}
