//go:build windows

package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes   = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryEx   = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64   = kernel32.NewProc("GetTickCount64")
	procGetDiskFreeSpace = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func fileTime(ft windows.Filetime) uint64 { return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime) }

func systemTimes() (idle, kernel, user uint64, err error) {
	var i, k, u windows.Filetime
	r, _, callErr := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	if r == 0 {
		return 0, 0, 0, callErr
	}
	return fileTime(i), fileTime(k), fileTime(u), nil
}

func cpuPercent(ctx context.Context) (float64, error) {
	i1, k1, u1, err := systemTimes()
	if err != nil {
		return 0, err
	}
	sample(ctx, time.Second)
	i2, k2, u2, err := systemTimes()
	if err != nil {
		return 0, err
	}
	total := (k2 - k1) + (u2 - u1) // kernel time includes idle
	if total == 0 {
		return 0, errors.New("no CPU counters")
	}
	return round1(float64(total-(i2-i1)) / float64(total) * 100), nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func memoryStatus() (*memoryStatusEx, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, err := procGlobalMemoryEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return nil, err
	}
	return &m, nil
}

func memoryPercent(ctx context.Context) (float64, error) {
	m, err := memoryStatus()
	if err != nil {
		return 0, err
	}
	return round1(float64(m.TotalPhys-m.AvailPhys) / float64(m.TotalPhys) * 100), nil
}

// Disks lists the fixed drives.
func Disks() []Disk {
	var out []Disk
	mask, _ := windows.GetLogicalDrives()
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		ptr, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(ptr) != windows.DRIVE_FIXED {
			continue
		}
		var free, total, totalFree uint64
		r, _, _ := procGetDiskFreeSpace.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
		if r == 0 || total == 0 {
			continue
		}
		out = append(out, Disk{Mount: root[:2], TotalGB: round1(float64(total) / (1 << 30)), FreeGB: round1(float64(free) / (1 << 30)),
			UsedPercent: round1(float64(total-free) / float64(total) * 100)})
	}
	return out
}

func uptime() time.Duration {
	r, _, _ := procGetTickCount64.Call()
	return time.Duration(r) * time.Millisecond
}

func rebootPending(ctx context.Context) bool {
	for _, path := range []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
	} {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE); err == nil {
			k.Close()
			return true
		}
	}
	return false
}

func serviceRunning(ctx context.Context, name string) bool {
	out, err := exec.CommandContext(ctx, "sc.exe", "query", name).Output()
	return err == nil && strings.Contains(string(out), "RUNNING")
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}
