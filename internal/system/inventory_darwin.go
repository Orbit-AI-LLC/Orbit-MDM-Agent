//go:build darwin

package system

import (
	"context"
	"encoding/json"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type profiler struct {
	Hardware []struct {
		MachineModel string `json:"machine_model"`
		MachineName  string `json:"machine_name"`
		Serial       string `json:"serial_number"`
		Memory       string `json:"physical_memory"`
		Chip         string `json:"chip_type"`
		UUID         string `json:"platform_UUID"`
	} `json:"SPHardwareDataType"`
	Applications []struct {
		Name    string `json:"_name"`
		Version string `json:"version"`
		Source  string `json:"obtained_from"`
		Path    string `json:"path"`
		Info    string `json:"info"`
	} `json:"SPApplicationsDataType"`
}

func hardware(ctx context.Context) profiler {
	var p profiler
	_ = json.Unmarshal([]byte(run(ctx, 30*time.Second, "system_profiler", "-json", "SPHardwareDataType")), &p)
	return p
}

// Who reads what the server needs to know at enrollment.
func Who(ctx context.Context) Identity {
	p := hardware(ctx)
	id := Identity{Hostname: hostname(), OS: "darwin", OSVersion: run(ctx, 5*time.Second, "sw_vers", "-productVersion"), Arch: runtime.GOARCH, Manufacturer: "Apple"}
	if len(p.Hardware) > 0 {
		id.Serial, id.Model, id.MachineID = p.Hardware[0].Serial, p.Hardware[0].MachineModel, p.Hardware[0].UUID
	}
	return id
}

// Inventory reads hardware, macOS, security state and every app.
func Inventory(ctx context.Context) map[string]any {
	p := hardware(ctx)
	hw := map[string]any{"manufacturer": "Apple"}
	if len(p.Hardware) > 0 {
		h := p.Hardware[0]
		hw["model"] = h.MachineModel
		hw["model_name"] = strings.TrimSpace(h.MachineName + " " + h.Chip)
		hw["serial"] = h.Serial
		if m := regexp.MustCompile(`([\d.]+) GB`).FindStringSubmatch(h.Memory); m != nil {
			hw["memory_gb"], _ = strconv.ParseFloat(m[1], 64)
		}
	}
	var apps profiler
	_ = json.Unmarshal([]byte(run(ctx, 3*time.Minute, "system_profiler", "-json", "SPApplicationsDataType")), &apps)
	software := make([]map[string]any, 0, len(apps.Applications))
	for _, app := range apps.Applications {
		if strings.HasPrefix(app.Path, "/System/") {
			continue
		}
		software = append(software, map[string]any{"name": app.Name, "version": app.Version, "id": app.Path, "publisher": app.Source})
	}
	filevault := strings.Contains(run(ctx, 10*time.Second, "fdesetup", "status"), "FileVault is On")
	firewall := strings.Contains(run(ctx, 10*time.Second, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate"), "enabled")
	return map[string]any{
		"hostname": hostname(),
		"hardware": hw,
		"os":       map[string]any{"name": "macOS", "version": run(ctx, 5*time.Second, "sw_vers", "-productVersion"), "build": run(ctx, 5*time.Second, "sw_vers", "-buildVersion")},
		"disks":    Disks(),
		"network":  Network(),
		"users":    signedInUsers(ctx),
		// XProtect is always on; Gatekeeper is what an administrator can turn off.
		"security": map[string]any{"encrypted": filevault, "firewall": firewall,
			"antivirus":   strings.Contains(run(ctx, 10*time.Second, "spctl", "--status"), "enabled"),
			"permissions": permissions(ctx)},
		"software": software,
	}
}
