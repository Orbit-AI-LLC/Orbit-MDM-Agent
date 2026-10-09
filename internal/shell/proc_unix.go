//go:build !windows

package shell

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// isolate starts the script in its own process group, so a timeout ends everything it started.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// asConsoleUser runs the script as whoever is signed in at the screen.
func asConsoleUser(args []string, dir string) ([]string, error) {
	user, uid := consoleUser()
	if user == "" || user == "root" {
		return nil, errors.New("nobody is signed in at this computer")
	}
	_ = os.Chmod(dir, 0o755)
	if runtime.GOOS == "darwin" {
		return append([]string{"launchctl", "asuser", uid, "sudo", "-u", user, "--"}, args...), nil
	}
	return append([]string{"runuser", "-u", user, "--"}, args...), nil
}

func consoleUser() (string, string) {
	if runtime.GOOS == "darwin" {
		info, err := os.Stat("/dev/console")
		if err != nil {
			return "", ""
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			uid := strconv.Itoa(int(st.Uid))
			out, err := exec.Command("id", "-un", uid).Output()
			if err == nil {
				return strings.TrimSpace(string(out)), uid
			}
		}
		return "", ""
	}
	out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] != "root" {
			return fields[2], fields[1]
		}
	}
	return "", ""
}
