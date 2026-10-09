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
	script := `display notification "` + who + ` connected to this screen through Orbit RMM." with title "Orbit RMM" sound name "Submarine"`
	_ = exec.Command("launchctl", "asuser", uid, "sudo", "-u", strings.TrimSpace(string(name)), "osascript", "-e", script).Start()
}
