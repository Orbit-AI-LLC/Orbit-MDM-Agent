//go:build !windows

package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunKeepsOutputAndExitCode(t *testing.T) {
	out, err := Run(context.Background(), Request{Shell: "sh", Body: "echo hello\necho oops >&2\nexit 3", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 3 || strings.TrimSpace(out.Stdout) != "hello" || strings.TrimSpace(out.Stderr) != "oops" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestTimeoutEndsTheScriptAndItsChildren(t *testing.T) {
	start := time.Now()
	out, err := Run(context.Background(), Request{Shell: "sh", Body: "sleep 30 &\nsleep 30", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !out.TimedOut || out.ExitCode != -1 || time.Since(start) > 10*time.Second {
		t.Fatalf("the script wasn't stopped: %+v after %s", out, time.Since(start))
	}
}

func TestOutputIsCapped(t *testing.T) {
	out, err := Run(context.Background(), Request{Shell: "sh", Body: "yes x | head -c 2000000", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stdout) > MaxOutput+100 || !strings.Contains(out.Stdout, "output cut") {
		t.Fatalf("output wasn't capped: %d bytes", len(out.Stdout))
	}
}

func TestUnknownShellsAreRefused(t *testing.T) {
	if _, err := Run(context.Background(), Request{Shell: "fish", Body: "echo"}); err == nil {
		t.Fatal("an unknown shell was accepted")
	}
}
