//go:build linux

package system

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"
)

func dmi(name string) string {
	data, err := os.ReadFile("/sys/class/dmi/id/" + name)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	if value == "To Be Filled By O.E.M." || value == "Default string" || value == "None" {
		return ""
	}
	return value
}

func osRelease() map[string]string {
	data, _ := os.ReadFile("/etc/os-release")
	return OSRelease(string(data))
}

// Who reads what the server needs to know at enrollment.
func Who(ctx context.Context) Identity {
	release := osRelease()
	machineID, _ := os.ReadFile("/etc/machine-id")
	return Identity{
		Hostname: hostname(), OS: "linux", OSVersion: release["VERSION_ID"], Arch: runtime.GOARCH,
		Serial: dmi("product_serial"), MachineID: strings.TrimSpace(string(machineID)),
		Manufacturer: dmi("sys_vendor"), Model: dmi("product_name"),
	}
}

func packages(ctx context.Context) []map[string]any {
	var out []map[string]any
	if text := run(ctx, 60*time.Second, "dpkg-query", "-W", "-f", "${Package}\t${Version}\t${Maintainer}\n"); text != "" {
		for _, line := range strings.Split(text, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) >= 2 {
				item := map[string]any{"name": parts[0], "id": parts[0], "version": parts[1]}
				if len(parts) == 3 {
					item["publisher"] = parts[2]
				}
				out = append(out, item)
			}
		}
		return out
	}
	if text := run(ctx, 60*time.Second, "rpm", "-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n"); text != "" {
		for _, line := range strings.Split(text, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) >= 2 {
				out = append(out, map[string]any{"name": parts[0], "id": parts[0], "version": parts[1], "publisher": strings.TrimPrefix(parts[len(parts)-1], "(none)")})
			}
		}
	}
	return out
}

func firewallOn(ctx context.Context) *bool {
	yes, no := true, false
	if out := run(ctx, 10*time.Second, "ufw", "status"); out != "" {
		if strings.Contains(out, "Status: active") {
			return &yes
		}
		return &no
	}
	if out := run(ctx, 10*time.Second, "firewall-cmd", "--state"); out != "" {
		if out == "running" {
			return &yes
		}
		return &no
	}
	if out := run(ctx, 10*time.Second, "nft", "list", "ruleset"); out != "" {
		return &yes
	}
	return nil
}

// Inventory reads hardware, OS, security state and every installed package.
func Inventory(ctx context.Context) map[string]any {
	release := osRelease()
	data, _ := os.ReadFile("/proc/meminfo")
	_, memGB, _ := MemInfo(string(data))
	encrypted := strings.Contains(run(ctx, 10*time.Second, "lsblk", "-o", "TYPE"), "crypt")
	services := strings.Split(run(ctx, 20*time.Second, "systemctl", "list-units", "--type=service", "--state=running", "--no-legend", "--plain"), "\n")
	running := make([]map[string]any, 0, len(services))
	for _, line := range services {
		if fields := strings.Fields(line); len(fields) > 0 {
			running = append(running, map[string]any{"name": strings.TrimSuffix(fields[0], ".service"), "running": true})
		}
	}
	return map[string]any{
		"hostname": hostname(),
		"hardware": map[string]any{"manufacturer": dmi("sys_vendor"), "model": dmi("product_name"), "model_name": dmi("product_name"),
			"serial": dmi("product_serial"), "memory_gb": memGB},
		"os":       map[string]any{"name": release["PRETTY_NAME"], "version": release["VERSION_ID"], "build": run(ctx, 5*time.Second, "uname", "-r")},
		"disks":    Disks(),
		"network":  Network(),
		"users":    signedInUsers(ctx),
		"services": running,
		"security": map[string]any{"encrypted": encrypted, "firewall": firewallOn(ctx), "antivirus": nil},
		"software": packages(ctx),
	}
}
