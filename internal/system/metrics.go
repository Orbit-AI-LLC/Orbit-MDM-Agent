package system

import (
	"context"
	"time"
)

// Metrics reads the computer's health for a check-in: CPU and memory in
// use, every disk, uptime, whether a restart is pending, and the state of
// any services the organization watches.
func Metrics(ctx context.Context, watch []string) map[string]any {
	out := map[string]any{}
	if cpu, err := cpuPercent(ctx); err == nil {
		out["cpu"] = cpu
	}
	if mem, err := memoryPercent(ctx); err == nil {
		out["memory"] = mem
	}
	disks := Disks()
	if len(disks) > 0 {
		out["disks"] = disks
	}
	if up := uptime(); up > 0 {
		out["uptime_hours"] = round1(up.Hours())
	}
	out["reboot_pending"] = rebootPending(ctx)
	if len(watch) > 0 {
		states := make([]map[string]any, 0, len(watch))
		for _, name := range watch {
			states = append(states, map[string]any{"name": name, "running": serviceRunning(ctx, name)})
		}
		out["watched_services"] = states
	}
	return out
}

// sample waits a moment between two readings of a counter.
func sample(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
