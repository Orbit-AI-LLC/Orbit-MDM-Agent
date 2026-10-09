//go:build darwin

package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func cpuPercent(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "top", "-l", "2", "-n", "0", "-s", "1").Output()
	if err != nil {
		return 0, err
	}
	busy, ok := TopCPU(string(out))
	if !ok {
		return 0, errors.New("no CPU reading")
	}
	return busy, nil
}

func sysctl(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func memoryPercent(ctx context.Context) (float64, error) {
	total, _ := strconv.ParseFloat(sysctl(ctx, "hw.memsize"), 64)
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, err
	}
	used, ok := VMStat(string(out), total)
	if !ok {
		return 0, errors.New("no memory reading")
	}
	return used, nil
}

func uptime() time.Duration {
	value := sysctl(context.Background(), "kern.boottime")
	m := regexp.MustCompile(`sec = (\d+)`).FindStringSubmatch(value)
	if m == nil {
		return 0
	}
	boot, _ := strconv.ParseInt(m[1], 10, 64)
	return time.Since(time.Unix(boot, 0))
}

func rebootPending(ctx context.Context) bool {
	// macOS has no system-wide flag; an update staged for restart leaves this behind.
	_, err := os.Stat("/var/db/.SoftwareUpdateRestartRequired")
	return err == nil
}

func serviceRunning(ctx context.Context, name string) bool {
	out, err := exec.CommandContext(ctx, "launchctl", "print", "system/"+name).Output()
	return err == nil && strings.Contains(string(out), "state = running")
}
