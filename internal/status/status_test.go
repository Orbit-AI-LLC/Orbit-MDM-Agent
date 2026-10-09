package status

import (
	"path/filepath"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	in := Status{
		Version: "1.2.3", OS: "darwin", Enrolled: true, Healthy: false,
		Permissions: map[string]string{"screen_recording": Denied},
		Issues:      []Issue{{ID: "screen_recording", Severity: Error, Title: "off", Fix: "open_screen_recording"}},
		Support:     Support{Name: "Help", Phone: "555", Email: "a@b.c"},
	}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != in.Version || out.Support != in.Support {
		t.Fatalf("round trip changed the status: %+v", out)
	}
	if len(out.Issues) != 1 || out.Issues[0].Fix != "open_screen_recording" {
		t.Fatalf("issues not preserved: %+v", out.Issues)
	}
	if out.Permissions["screen_recording"] != Denied {
		t.Fatalf("permissions not preserved: %+v", out.Permissions)
	}
}

func TestWriteDefaultsIssuesToEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := Write(path, Status{OS: "windows"}); err != nil {
		t.Fatal(err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Issues == nil {
		t.Fatal("Issues should be an empty list, not null, so the tray can iterate it")
	}
}

func TestSupportEmpty(t *testing.T) {
	if !(Support{}).Empty() {
		t.Fatal("a blank support should be empty")
	}
	if (Support{Phone: "555"}).Empty() {
		t.Fatal("a support with a phone is not empty")
	}
}
