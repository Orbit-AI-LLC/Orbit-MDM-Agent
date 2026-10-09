// Package system reads a computer's health, inventory and updates, and
// changes them (installs, restarts) on Windows, macOS and Linux.
//
// The parsers in this file read the output of the operating systems' own
// tools; they have no build tags so they are tested on every platform.
package system

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Update is one update a computer still needs.
type Update struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Severity       string  `json:"severity"`
	Category       string  `json:"category,omitempty"`
	KB             string  `json:"kb,omitempty"`
	SizeMB         float64 `json:"size_mb,omitempty"`
	RebootRequired bool    `json:"reboot_required"`
}

// Disk is one mounted volume.
type Disk struct {
	Mount       string  `json:"mount"`
	TotalGB     float64 `json:"total_gb"`
	FreeGB      float64 `json:"free_gb"`
	UsedPercent float64 `json:"used_percent"`
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// CPUTimes reads the first "cpu" line of /proc/stat: idle and total jiffies.
func CPUTimes(stat string) (idle, total uint64, ok bool) {
	for _, line := range strings.Split(stat, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		for i, f := range fields[1:] {
			n, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			total += n
			if i == 3 || i == 4 { // idle, iowait
				idle += n
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}

// MemInfo reads /proc/meminfo: the percentage of memory in use.
func MemInfo(text string) (usedPercent float64, totalGB float64, ok bool) {
	values := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			values[name] = v
		}
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total == 0 {
		return 0, 0, false
	}
	return round1((total - available) / total * 100), round1(total / 1024 / 1024), true
}

// VMStat reads macOS's vm_stat: the percentage of memory in use, given the total in bytes.
func VMStat(text string, totalBytes float64) (float64, bool) {
	page := 16384.0
	if m := regexp.MustCompile(`page size of (\d+) bytes`).FindStringSubmatch(text); m != nil {
		page, _ = strconv.ParseFloat(m[1], 64)
	}
	pages := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), "."), 64)
		if err == nil {
			pages[strings.TrimSpace(name)] = v
		}
	}
	if totalBytes <= 0 {
		return 0, false
	}
	used := (pages["Pages active"] + pages["Pages wired down"] + pages["Pages occupied by compressor"]) * page
	return round1(used / totalBytes * 100), true
}

// TopCPU reads the last "CPU usage" line of macOS's `top -l 2`: percent busy.
func TopCPU(text string) (float64, bool) {
	re := regexp.MustCompile(`CPU usage: [\d.]+% user, [\d.]+% sys, ([\d.]+)% idle`)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	idle, err := strconv.ParseFloat(matches[len(matches)-1][1], 64)
	if err != nil {
		return 0, false
	}
	return round1(100 - idle), true
}

// OSRelease reads /etc/os-release.
func OSRelease(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			out[key] = strings.Trim(value, `"'`)
		}
	}
	return out
}

// AptUpgrades reads `apt-get -s upgrade`: what would be upgraded.
// Packages from a *-security pocket are rated important.
func AptUpgrades(text string) []Update {
	re := regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]+)\] )?\((\S+) ([^)]*)\)`)
	var out []Update
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		m := re.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		severity := "moderate"
		if strings.Contains(m[4], "-security") {
			severity = "important"
		}
		out = append(out, Update{ID: m[1], Title: m[1] + " " + m[3], Severity: severity, Category: "Package"})
	}
	return out
}

// DnfUpdates reads `dnf -q check-update`: name.arch, version, repository.
func DnfUpdates(text string, security map[string]string) []Update {
	var out []Update
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || strings.HasPrefix(line, " ") || !strings.Contains(fields[0], ".") {
			continue
		}
		name := fields[0][:strings.LastIndex(fields[0], ".")]
		severity := "moderate"
		if s, ok := security[name]; ok {
			severity = s
		}
		out = append(out, Update{ID: name, Title: name + " " + fields[1], Severity: severity, Category: "Package"})
	}
	return out
}

// DnfSecurity reads `dnf -q updateinfo list --security`: package severities.
func DnfSecurity(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		severity := strings.ToLower(strings.TrimSuffix(fields[1], "/Sec."))
		switch severity {
		case "critical", "important", "moderate", "low":
		default:
			severity = "important"
		}
		pkg := fields[2]
		// name-version-release.arch → name
		parts := strings.Split(pkg, "-")
		if len(parts) >= 3 {
			pkg = strings.Join(parts[:len(parts)-2], "-")
		}
		out[pkg] = severity
	}
	return out
}

// SoftwareUpdates reads macOS's `softwareupdate -l`.
func SoftwareUpdates(text string) []Update {
	var out []Update
	var current *Update
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "* Label:") {
			label := strings.TrimSpace(strings.TrimPrefix(trimmed, "* Label:"))
			out = append(out, Update{ID: label, Title: label, Severity: "important", Category: "Software Update"})
			current = &out[len(out)-1]
			continue
		}
		if current == nil || !strings.HasPrefix(trimmed, "Title:") {
			continue
		}
		for _, part := range strings.Split(trimmed, ",") {
			key, value, found := strings.Cut(strings.TrimSpace(part), ":")
			if !found {
				continue
			}
			value = strings.TrimSpace(value)
			switch key {
			case "Title":
				current.Title = value
			case "Version":
				if !strings.Contains(current.Title, value) {
					current.Title += " " + value
				}
			case "Size":
				if kib, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(value, "KiB"), "K"), 64); err == nil {
					current.SizeMB = round1(kib / 1024)
				}
			case "Action":
				current.RebootRequired = value == "restart"
			case "Recommended":
				if value != "YES" {
					current.Severity = "moderate"
				}
			}
		}
		if strings.Contains(current.Title, "Security") || strings.Contains(current.Title, "Rapid") {
			current.Severity = "critical"
		}
	}
	return out
}

// Severity turns Windows Update's MSRC rating into Orbit's scale.
func Severity(msrc, category string) string {
	switch strings.ToLower(msrc) {
	case "critical":
		return "critical"
	case "important":
		return "important"
	case "moderate":
		return "moderate"
	case "low":
		return "low"
	}
	if strings.Contains(strings.ToLower(category), "security") {
		return "important"
	}
	if strings.Contains(strings.ToLower(category), "definition") {
		return "low"
	}
	return "unknown"
}
