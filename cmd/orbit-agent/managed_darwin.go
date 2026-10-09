//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

// managedPreferences is where a configuration profile's custom settings for
// the ai.orbit.agent domain land (Jamf, Intune, Kandji, Orbit MDM).
var managedPreferences = "/Library/Managed Preferences/ai.orbit.agent"

// managedEnrollment is the server and install token an MDM set for the agent:
// the Server and Token keys of its managed preferences.
func managedEnrollment() (server, token string) {
	read := func(key string) string {
		out, err := exec.Command("/usr/bin/defaults", "read", managedPreferences, key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return read("Server"), read("Token")
}
