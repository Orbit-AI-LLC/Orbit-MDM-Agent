//go:build linux

package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ScanUpdates lists the package updates waiting, from apt, dnf or zypper.
func ScanUpdates(ctx context.Context) ([]Update, error) {
	switch {
	case has("apt-get"):
		_ = exec.CommandContext(ctx, "apt-get", "update", "-qq").Run()
		out, err := exec.CommandContext(ctx, "apt-get", "-s", "-o", "Debug::NoLocking=1", "upgrade").Output()
		if err != nil {
			return nil, err
		}
		return AptUpgrades(string(out)), nil
	case has("dnf"):
		security := DnfSecurity(run(ctx, 5*time.Minute, "dnf", "-q", "updateinfo", "list", "--security"))
		// check-update exits 100 when there are updates.
		out, _ := exec.CommandContext(ctx, "dnf", "-q", "check-update").Output()
		return DnfUpdates(string(out), security), nil
	case has("zypper"):
		out, err := exec.CommandContext(ctx, "zypper", "-q", "-n", "list-updates").Output()
		if err != nil {
			return nil, err
		}
		var updates []Update
		for _, line := range strings.Split(string(out), "\n") {
			cols := strings.Split(line, "|")
			if len(cols) >= 5 && strings.TrimSpace(cols[0]) == "v" {
				name := strings.TrimSpace(cols[2])
				updates = append(updates, Update{ID: name, Title: name + " " + strings.TrimSpace(cols[4]), Severity: "moderate", Category: "Package"})
			}
		}
		return updates, nil
	}
	return nil, fmt.Errorf("no supported package manager")
}

// InstallUpdates upgrades the named packages (all when ids is empty).
func InstallUpdates(ctx context.Context, ids []string) ([]map[string]any, string, error) {
	var cmd *exec.Cmd
	switch {
	case has("apt-get"):
		args := []string{"-y", "-o", "Dpkg::Options::=--force-confold"}
		if len(ids) == 0 {
			args = append([]string{"upgrade"}, args...)
		} else {
			args = append(append([]string{"install", "--only-upgrade"}, args...), ids...)
		}
		cmd = exec.CommandContext(ctx, "apt-get", args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	case has("dnf"):
		cmd = exec.CommandContext(ctx, "dnf", append([]string{"-y", "upgrade"}, ids...)...)
	case has("zypper"):
		cmd = exec.CommandContext(ctx, "zypper", append([]string{"-n", "update"}, ids...)...)
	default:
		return nil, "", fmt.Errorf("no supported package manager")
	}
	out, err := cmd.CombinedOutput()
	results := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		results = append(results, map[string]any{"id": id, "ok": err == nil})
	}
	return results, string(out), err
}
