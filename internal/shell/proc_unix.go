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

// runAsTempBase is the directory a RunAs="user" script's temp folder is made in.
// /tmp is world-traversable (sticky) on macOS and Linux, so the signed-in user
// can reach their own folder once it's handed to them (see asConsoleUser),
// unlike the agent's default temp root, which can be private to root.
func runAsTempBase() string { return "/tmp" }

// asConsoleUser runs the script as whoever is signed in at the screen. The agent
// runs as root and wrote the script, so it hands the script's folder and the
// script itself to that user; otherwise the interpreter, running as them, can't
// read it.
func asConsoleUser(args []string, dir, file string) ([]string, error) {
	user, uid := consoleUser()
	if user == "" || user == "root" {
		return nil, errors.New("nobody is signed in at this computer")
	}
	if n, err := strconv.Atoi(uid); err == nil {
		_ = os.Chown(dir, n, -1)
		_ = os.Chown(file, n, -1)
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(file, 0o600)
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
