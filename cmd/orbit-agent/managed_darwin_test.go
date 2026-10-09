//go:build darwin

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestManagedEnrollmentReadsTheProfilesSettings(t *testing.T) {
	saved := managedPreferences
	defer func() { managedPreferences = saved }()
	managedPreferences = filepath.Join(t.TempDir(), "ai.orbit.agent")

	if server, token := managedEnrollment(); server != "" || token != "" {
		t.Fatalf("no profile: got %q, %q", server, token)
	}
	for key, value := range map[string]string{"Server": "https://mdm.example.com", "Token": "orbe_x1_secret"} {
		if out, err := exec.Command("/usr/bin/defaults", "write", managedPreferences, key, "-string", value).CombinedOutput(); err != nil {
			t.Fatalf("defaults write: %v: %s", err, out)
		}
	}
	if server, token := managedEnrollment(); server != "https://mdm.example.com" || token != "orbe_x1_secret" {
		t.Fatalf("got %q, %q", server, token)
	}
}
