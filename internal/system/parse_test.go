package system

import "testing"

func TestCPUTimes(t *testing.T) {
	idle, total, ok := CPUTimes("cpu  4705 356 584 3699 23 23 0 0 0 0\ncpu0 1393280 32966 572056 13343292 6130 0 17875 0 0 0\n")
	if !ok || idle != 3722 || total != 9390 {
		t.Fatalf("got idle=%d total=%d ok=%v", idle, total, ok)
	}
}

func TestMemInfo(t *testing.T) {
	used, total, ok := MemInfo("MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    4096000 kB\n")
	if !ok || used != 75 || total != 15.6 {
		t.Fatalf("got used=%v total=%v", used, total)
	}
}

func TestVMStatAndTop(t *testing.T) {
	text := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10000.\nPages active: 300000.\nPages wired down: 100000.\nPages occupied by compressor: 100000.\n"
	used, ok := VMStat(text, 16*1024*1024*1024)
	if !ok || used != 47.7 {
		t.Fatalf("got %v", used)
	}
	busy, ok := TopCPU("CPU usage: 3.1% user, 2.2% sys, 94.7% idle\nCPU usage: 10.5% user, 4.5% sys, 85.0% idle\n")
	if !ok || busy != 15 {
		t.Fatalf("got %v", busy)
	}
}

func TestAptUpgrades(t *testing.T) {
	text := `Reading package lists...
Inst openssl [3.0.13-0ubuntu3.1] (3.0.13-0ubuntu3.4 Ubuntu:24.04/noble-security [amd64])
Inst curl [8.5.0-2ubuntu10.1] (8.5.0-2ubuntu10.4 Ubuntu:24.04/noble-updates [amd64])
Conf openssl (3.0.13-0ubuntu3.4 Ubuntu:24.04/noble-security [amd64])`
	got := AptUpgrades(text)
	if len(got) != 2 || got[0].ID != "openssl" || got[0].Severity != "important" || got[1].Severity != "moderate" {
		t.Fatalf("got %+v", got)
	}
}

func TestDnf(t *testing.T) {
	security := DnfSecurity("RHSA-2024:1234 Important/Sec. openssl-libs-3.0.7-27.el9.x86_64\nRHSA-2024:5678 Critical/Sec. kernel-5.14.0-427.el9.x86_64\n")
	got := DnfUpdates("\nopenssl-libs.x86_64   1:3.0.7-27.el9   baseos\nvim-enhanced.x86_64   2:8.2.2637-21.el9   appstream\n", security)
	if len(got) != 2 || got[0].Severity != "important" || got[1].Severity != "moderate" || security["kernel"] != "critical" {
		t.Fatalf("got %+v %+v", got, security)
	}
}

func TestSoftwareUpdates(t *testing.T) {
	text := `Software Update Tool

Finding available software
Software Update found the following new or updated software:
* Label: macOS Sequoia 15.1-24B83
	Title: macOS Sequoia 15.1, Version: 15.1, Size: 3207816KiB, Recommended: YES, Action: restart,
* Label: Background Security Improvement 15.0.1 (a)
	Title: Background Security Improvement, Version: 15.0.1 (a), Size: 2048KiB, Recommended: YES, Action: none,
`
	got := SoftwareUpdates(text)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != "macOS Sequoia 15.1-24B83" || !got[0].RebootRequired || got[0].SizeMB != 3132.6 || got[0].Severity != "important" {
		t.Fatalf("got %+v", got[0])
	}
	if got[1].Severity != "critical" || got[1].RebootRequired {
		t.Fatalf("got %+v", got[1])
	}
}

func TestSeverity(t *testing.T) {
	cases := map[[2]string]string{
		{"Critical", ""}: "critical", {"", "Security Updates"}: "important", {"", "Definition Updates"}: "low", {"", "Drivers"}: "unknown",
	}
	for in, want := range cases {
		if got := Severity(in[0], in[1]); got != want {
			t.Errorf("Severity(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
