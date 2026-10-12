package system

import (
	"context"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Identity is what the agent tells the server when it enrolls.
type Identity struct {
	Hostname     string
	OS           string
	OSVersion    string
	Arch         string
	Serial       string
	MachineID    string
	Manufacturer string
	Model        string
}

// Network lists the computer's network interfaces with their addresses.
func Network() []map[string]any {
	var out []map[string]any
	interfaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var ips []string
		for _, addr := range addrs {
			ips = append(ips, strings.Split(addr.String(), "/")[0])
		}
		if len(ips) == 0 {
			continue
		}
		out = append(out, map[string]any{"name": iface.Name, "mac": iface.HardwareAddr.String(), "addresses": ips})
	}
	return out
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) string {
	out, err := runErr(ctx, timeout, name, args...)
	if err != nil {
		return ""
	}
	return out
}

// runErr is run but keeps the command's error, so a caller can tell a failed
// command apart from one that succeeded with no output.
func runErr(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// signedInUsers lists who is signed in, from `who` on macOS and Linux.
func signedInUsers(ctx context.Context) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	seen := map[string]bool{}
	var users []string
	for _, line := range strings.Split(run(ctx, 10*time.Second, "who"), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && !seen[fields[0]] {
			seen[fields[0]] = true
			users = append(users, fields[0])
		}
	}
	return users
}
