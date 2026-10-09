//go:build !windows

package system

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// Disks lists the real, mounted volumes.
func Disks() []Disk {
	var mounts []string
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		skip := map[string]bool{"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true, "cgroup": true,
			"cgroup2": true, "overlay": true, "squashfs": true, "securityfs": true, "debugfs": true, "tracefs": true,
			"mqueue": true, "hugetlbfs": true, "pstore": true, "bpf": true, "autofs": true, "fusectl": true, "configfs": true,
			"efivarfs": true, "binfmt_misc": true, "nsfs": true, "ramfs": true, "rpc_pipefs": true}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		seen := map[string]bool{}
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 || skip[fields[2]] || seen[fields[0]] || strings.HasPrefix(fields[1], "/snap/") {
				continue
			}
			seen[fields[0]] = true
			mounts = append(mounts, fields[1])
		}
	} else {
		mounts = []string{"/", "/System/Volumes/Data"}
	}
	var out []Disk
	for _, mount := range mounts {
		var st syscall.Statfs_t
		if syscall.Statfs(mount, &st) != nil || st.Blocks == 0 {
			continue
		}
		total := float64(st.Blocks) * float64(st.Bsize)
		free := float64(st.Bavail) * float64(st.Bsize)
		if total < 1<<30 {
			continue
		}
		out = append(out, Disk{Mount: mount, TotalGB: round1(total / (1 << 30)), FreeGB: round1(free / (1 << 30)),
			UsedPercent: round1((total - free) / total * 100)})
	}
	return out
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}
