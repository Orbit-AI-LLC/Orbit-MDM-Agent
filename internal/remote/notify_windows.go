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
	cmd := exec.Command("msg", "*", "/TIME:20", who+" connected to this computer's screen through Orbit RMM.")
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }() // reap the helper so its handle isn't leaked
	}
}
