//go:build windows

package shell

import (
	"errors"
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup, HideWindow: true}
}

// killTree ends the script and everything it started (taskkill /T).
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}

// asConsoleUser isn't available on Windows yet: the agent runs as SYSTEM.
func asConsoleUser(args []string, dir string) ([]string, error) {
	return nil, errors.New("running as the signed-in user isn't supported on Windows yet; run it as System")
}
