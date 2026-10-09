//go:build !windows

package remote

import (
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// unixTerminal is a shell on a Unix pseudo-terminal (macOS, Linux).
type unixTerminal struct {
	pty  *os.File
	cmd  *exec.Cmd
	once sync.Once
	code int
	done chan struct{}
}

// openTerminal starts the shell as a login shell on a new PTY, as whoever the
// agent runs as (root). The shell is interactive because it has a terminal.
func openTerminal(shell string, cols, rows uint16) (terminal, error) {
	path := map[string]string{
		"bash": "/bin/bash", "zsh": "/bin/zsh", "sh": "/bin/sh",
	}[shell]
	if path == "" {
		path = "/bin/sh"
	}
	cmd := exec.Command(path, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, err
	}
	t := &unixTerminal{pty: f, cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		if exit, ok := err.(*exec.ExitError); ok {
			t.code = exit.ExitCode()
		} else if err != nil {
			t.code = 1
		}
		close(t.done)
	}()
	return t, nil
}

func (t *unixTerminal) Read(p []byte) (int, error)  { return t.pty.Read(p) }
func (t *unixTerminal) Write(p []byte) (int, error) { return t.pty.Write(p) }

func (t *unixTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.pty, &pty.Winsize{Rows: rows, Cols: cols})
}

func (t *unixTerminal) Wait() int {
	<-t.done
	return t.code
}

func (t *unixTerminal) Close() error {
	t.once.Do(func() {
		_ = t.pty.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
	})
	return nil
}
