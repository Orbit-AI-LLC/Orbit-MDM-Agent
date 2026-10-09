//go:build windows

package remote

import "os/exec"

// notifyViewing tells whoever is signed in that their screen is being viewed.
// msg.exe reaches the interactive session from SYSTEM.
func notifyViewing(by string) {
	who := by
	if who == "" {
		who = "An administrator"
	}
	_ = exec.Command("msg", "*", "/TIME:20", who+" connected to this computer's screen through Orbit RMM.").Start()
}
