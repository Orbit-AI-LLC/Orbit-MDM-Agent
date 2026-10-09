//go:build linux

package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func cpuPercent(ctx context.Context) (float64, error) {
	read := func() (uint64, uint64, bool) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0, false
		}
		return CPUTimes(string(data))
	}
	idle1, total1, ok1 := read()
	sample(ctx, time.Second)
	idle2, total2, ok2 := read()
	if !ok1 || !ok2 || total2 <= total1 {
		return 0, errors.New("no CPU counters")
	}
	busy := float64((total2-total1)-(idle2-idle1)) / float64(total2-total1) * 100
	return round1(busy), nil
}

func memoryPercent(ctx context.Context) (float64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	used, _, ok := MemInfo(string(data))
	if !ok {
		return 0, errors.New("no memory counters")
	}
	return used, nil
}

func uptime() time.Duration {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	seconds, _ := strconv.ParseFloat(fields[0], 64)
	return time.Duration(seconds * float64(time.Second))
}

func rebootPending(ctx context.Context) bool {
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		return true
	}
	if _, err := exec.LookPath("needs-restarting"); err == nil {
		return exec.CommandContext(ctx, "needs-restarting", "-r").Run() != nil
	}
	return false
}

func serviceRunning(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", name).Run() == nil
}
