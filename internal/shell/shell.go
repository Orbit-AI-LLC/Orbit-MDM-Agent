// Package shell runs a script in the shell it was written for, with a
// timeout that ends it and everything it started, and keeps what it printed.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// MaxOutput is how much of each stream is kept.
const MaxOutput = 512 << 10

// Output is what a script did.
type Output struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
}

// Request is a script to run.
type Request struct {
	Shell   string
	Body    string
	Timeout time.Duration
	// RunAs "user" runs as the person signed in at the console (macOS and Linux).
	RunAs string
}

type capped struct {
	buf bytes.Buffer
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := MaxOutput - c.buf.Len()
	if room <= 0 {
		c.cut = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.cut = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *capped) String() string {
	if c.cut {
		return c.buf.String() + "\n… (output cut at 512 KB)"
	}
	return c.buf.String()
}

var extensions = map[string]string{
	"powershell": ".ps1", "cmd": ".cmd", "bash": ".sh", "sh": ".sh", "zsh": ".zsh", "python": ".py",
}

// command is the program and arguments that run a script file in a shell.
func command(shell, file string) ([]string, error) {
	switch shell {
	case "powershell":
		if runtime.GOOS == "windows" {
			return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file}, nil
		}
		return []string{"pwsh", "-NoProfile", "-NonInteractive", "-File", file}, nil
	case "cmd":
		if runtime.GOOS != "windows" {
			return nil, errors.New("Command Prompt scripts run on Windows only")
		}
		return []string{"cmd.exe", "/d", "/c", file}, nil
	case "bash":
		return []string{"/bin/bash", file}, nil
	case "sh":
		return []string{"/bin/sh", file}, nil
	case "zsh":
		return []string{"/bin/zsh", file}, nil
	case "python":
		if runtime.GOOS == "windows" {
			return []string{"py", "-3", file}, nil
		}
		return []string{"python3", file}, nil
	}
	return nil, fmt.Errorf("unknown shell %q", shell)
}

// Run runs a script and waits for it, up to its timeout.
func Run(ctx context.Context, req Request) (*Output, error) {
	ext, ok := extensions[req.Shell]
	if !ok {
		return nil, fmt.Errorf("unknown shell %q", req.Shell)
	}
	dir, err := os.MkdirTemp("", "orbit-task-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "script"+ext)
	body := req.Body
	if req.Shell == "cmd" {
		body = "@echo off\r\n" + body
	}
	if err := os.WriteFile(file, []byte(body), 0o700); err != nil {
		return nil, err
	}
	args, err := command(req.Shell, file)
	if err != nil {
		return nil, err
	}
	if req.RunAs == "user" {
		args, err = asConsoleUser(args, dir)
		if err != nil {
			return nil, err
		}
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	var stdout, stderr capped
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	isolate(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	out := &Output{}
	select {
	case err = <-done:
	case <-ctx.Done():
		killTree(cmd)
		err = <-done
		out.TimedOut = true
	}
	out.Stdout, out.Stderr = stdout.String(), stderr.String()
	var exitErr *exec.ExitError
	switch {
	case out.TimedOut:
		out.ExitCode = -1
		out.Stderr += fmt.Sprintf("\nStopped after %s.", timeout)
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	case err != nil:
		return nil, err
	}
	return out, nil
}

// Exec runs a program directly (no script file) and returns its output.
func Exec(ctx context.Context, timeout time.Duration, name string, args ...string) (*Output, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr capped
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := &Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		out.ExitCode, out.TimedOut = -1, true
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	case err != nil:
		return nil, err
	}
	return out, nil
}
