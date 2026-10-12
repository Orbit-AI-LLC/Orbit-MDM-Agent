//go:build darwin

package remote

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// notifyViewing shows a notification in the signed-in person's session that
// their screen is being viewed. The agent is root, so it runs osascript as
// the console user in their GUI session.
func notifyViewing(by string) {
	who := by
	if who == "" {
		who = "An administrator"
	}
	info, err := os.Stat("/dev/console")
	if err != nil {
		return
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return
	}
	uid := strconv.FormatUint(uint64(st.Uid), 10)
	name, err := exec.Command("id", "-un", uid).Output()
	if err != nil {
		return
	}
	// who comes from the signed task; escape it so a quote or backslash in the
	// admin's name can't break (or inject into) the AppleScript.
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(who)
	script := `display notification "` + esc + ` connected to this screen through Orbit RMM." with title "Orbit RMM" sound name "Submarine"`
	cmd := exec.Command("launchctl", "asuser", uid, "sudo", "-u", strings.TrimSpace(string(name)), "osascript", "-e", script)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }() // reap the helper so it doesn't linger as a zombie
	}
}
