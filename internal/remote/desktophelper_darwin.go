//go:build darwin

package remote

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The remote desktop's screen capture must run in the signed-in person's GUI
// session, not in the root launch daemon: macOS only lets a process that is in an
// active session be granted Screen Recording, and only such a process can raise
// the permission prompt for someone to approve — which is what lets the agent
// work without an MDM pushing a PPPC profile. So the daemon no longer captures the
// screen itself; a per-user LaunchAgent (ai.orbit.agent.desktop, the same signed
// binary run with `desktop-helper`) hosts the built-in VNC server on a unix
// socket, and the daemon dials it when a desktop session opens.
//
// The helper reuses the service's code-signing identity (ai.orbit.agent), so the
// Screen Recording grant it is given also covers a managed Mac's PPPC profile,
// and there is no second bundle to sign.

// desktopSocketPath is where the per-user helper listens. One per uid, under the
// sticky world-writable /private/tmp so the user can create it; 0600 and
// user-owned, so only that user and root (the daemon) can connect.
func desktopSocketPath(uid string) string {
	return filepath.Join("/private/tmp", "orbit-desktop-"+uid+".sock")
}

// RunDesktopHelper runs the user-session half of the remote desktop: it hosts the
// built-in VNC/RFB server on the unix socket and holds Screen Recording. The root
// daemon dials the socket when a session opens. It provokes the Screen Recording
// prompt on start (and so, after the LaunchAgent's KeepAlive restarts it, again
// until the person grants it). It returns when ctx ends.
func RunDesktopHelper(ctx context.Context, logger *log.Logger) error {
	if logger == nil {
		logger = log.New(os.Stderr, "orbit-desktop: ", log.LstdFlags)
	}
	uid := strconv.Itoa(os.Getuid())
	sock := desktopSocketPath(uid)
	_ = os.Remove(sock) // a stale socket from a previous run would block Listen
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("desktop helper couldn't listen on %s: %w", sock, err)
	}
	defer ln.Close()
	defer os.Remove(sock)
	_ = os.Chmod(sock, 0o600)
	logger.Printf("desktop helper listening on %s", sock)

	// List the agent under Screen Recording (the remote view) and Accessibility
	// (remote keyboard and mouse) and raise their prompts while the decisions are
	// still open. Running here, in the GUI session, is what lets the person grant
	// them without an MDM.
	provokeScreenRecording(ctx)
	requestAccessibility()

	go func() { <-ctx.Done(); ln.Close() }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveDesktopConn(ctx, conn, logger)
	}
}

// serveDesktopConn captures the screen for one dialed connection (the daemon,
// proxying the browser's noVNC) until it closes.
func serveDesktopConn(ctx context.Context, conn net.Conn, logger *log.Logger) {
	defer conn.Close()
	screen, err := newMacScreen()
	if err != nil {
		logger.Printf("can't capture the screen (Screen Recording may be off): %v", err)
		return
	}
	// serveVNC closes the screen when it ends.
	_ = serveVNC(ctx, conn, screen)
}

// provokeScreenRecording takes a throwaway capture to list the agent under Screen
// Recording and raise the prompt.
func provokeScreenRecording(ctx context.Context) {
	dir, err := os.MkdirTemp("", "orbit-desktop-perm-")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(c, "screencapture", "-x", "-t", "png", "-D", "1", filepath.Join(dir, "probe.png")).Run()
}

// dialDesktopHelper connects to the signed-in person's desktop helper, or returns
// nil when there is none (nobody signed in, or the helper isn't up yet).
func dialDesktopHelper(ctx context.Context) net.Conn {
	uid := consoleUID()
	if uid == "" {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(c, "unix", desktopSocketPath(uid))
	if err != nil {
		return nil
	}
	return conn
}

// consoleUID is the uid of whoever is signed in at the screen, or "" when that's
// nobody (the login window, where /dev/console belongs to root).
func consoleUID() string {
	out, err := exec.Command("stat", "-f%Su", "/dev/console").Output()
	if err != nil {
		return ""
	}
	user := strings.TrimSpace(string(out))
	if user == "" || user == "root" {
		return ""
	}
	id, err := exec.Command("id", "-u", user).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(id))
}
