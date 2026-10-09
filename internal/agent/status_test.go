package agent

import (
	"testing"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/status"
)

func TestIssuesWhenCheckinFails(t *testing.T) {
	got := issues(false, map[string]any{}, nil)
	if len(got) != 1 || got[0].ID != "checkin" || got[0].Severity != status.Error {
		t.Fatalf("a failed check-in should be one error issue, got %+v", got)
	}
	if healthy(got) {
		t.Fatal("an error issue means not healthy")
	}
}

func TestIssuesFromPermissions(t *testing.T) {
	perms := map[string]string{
		"screen_recording": status.Denied,
		"full_disk_access": status.Denied,
		"accessibility":    status.Denied,
	}
	got := issues(true, map[string]any{"reboot_pending": true}, perms)
	by := map[string]status.Issue{}
	for _, i := range got {
		by[i.ID] = i
	}
	if by["screen_recording"].Severity != status.Error || by["screen_recording"].Fix != "open_screen_recording" {
		t.Fatalf("screen recording should be a fixable error: %+v", by["screen_recording"])
	}
	if by["full_disk_access"].Severity != status.Warning {
		t.Fatalf("full disk access should be a warning: %+v", by["full_disk_access"])
	}
	if by["accessibility"].Severity != status.Info {
		t.Fatalf("accessibility should be info only: %+v", by["accessibility"])
	}
	if _, ok := by["reboot"]; !ok {
		t.Fatal("a pending reboot should raise an issue")
	}
	if healthy(got) {
		t.Fatal("screen recording denied is an error, so not healthy")
	}
}

func TestHealthyWhenGranted(t *testing.T) {
	perms := map[string]string{
		"screen_recording": status.Granted,
		"full_disk_access": status.Granted,
		"accessibility":    status.Granted,
	}
	got := issues(true, map[string]any{"reboot_pending": false}, perms)
	if len(got) != 0 {
		t.Fatalf("all granted and no reboot should be no issues, got %+v", got)
	}
	if !healthy(got) {
		t.Fatal("no issues means healthy")
	}
}
