//go:build linux

package remote

import (
	"os"
	"os/exec"
)

// notifyViewing shows a desktop notification, best effort, in the signed-in
// person's session that their screen is being viewed.
func notifyViewing(by string) {
	who := by
	if who == "" {
		who = "An administrator"
	}
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	session := graphicalSession()
	if session.name == "" || session.user == "" {
		return
	}
	cmd := exec.Command("runuser", "-u", session.name, "--", "notify-send", "-u", "critical", "Orbit RMM",
		who+" connected to this screen through Orbit RMM.")
	cmd.Env = append(os.Environ(),
		"DISPLAY="+session.display,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+session.user+"/bus",
		"XDG_RUNTIME_DIR=/run/user/"+session.user)
	_ = cmd.Start()
}
