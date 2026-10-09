//go:build linux

package remote

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// dialDesktop connects to a VNC server already running, or, failing that,
// starts x11vnc on the graphical session signed in at the screen and connects
// to that. x11vnc binds to localhost, so only this agent reaches it, and is
// stopped when the session ends.
func dialDesktop(ctx context.Context, opt Options) (net.Conn, func(), error) {
	if conn := dialLocal(ctx, []int{5900, 5901, 5902}); conn != nil {
		return conn, func() {}, nil
	}
	if _, err := exec.LookPath("x11vnc"); err != nil {
		return nil, nil, errors.New("No VNC server is answering, and x11vnc isn't installed. Install it (apt install x11vnc, or dnf install x11vnc), or, on Wayland, turn on the desktop's own screen sharing.")
	}
	session := graphicalSession()
	if session.display == "" {
		return nil, nil, errors.New("Nobody is signed in at the screen, or the desktop is Wayland without screen sharing on. Turn on the desktop's own screen sharing instead.")
	}

	const port = 5900
	cmd := exec.Command("x11vnc",
		"-display", session.display, "-auth", session.xauthority,
		"-rfbport", "5900", "-localhost", "-nopw", "-forever", "-shared", "-noxdamage", "-quiet")
	cmd.Env = append(os.Environ(), "DISPLAY="+session.display, "XAUTHORITY="+session.xauthority)
	if err := cmd.Start(); err != nil {
		return nil, nil, errors.New("Couldn't start x11vnc: " + err.Error())
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}

	// x11vnc needs a moment before it listens.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if conn := dialLocal(ctx, []int{port}); conn != nil {
			return conn, stop, nil
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	stop()
	return nil, nil, errors.New("x11vnc started but didn't answer. The desktop may be Wayland; turn on its own screen sharing instead.")
}

type graphical struct {
	display    string
	xauthority string
	user       string // the numeric uid
	name       string // the user name
}

// graphicalSession finds the active X session's display and its X authority
// file, from systemd-logind.
func graphicalSession() graphical {
	out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return graphical{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		id := fields[0]
		props := sessionProps(id)
		if props["Active"] != "yes" || props["Display"] == "" {
			continue
		}
		g := graphical{display: props["Display"], xauthority: xauthorityFor(props), user: props["User"], name: props["Name"]}
		if g.xauthority != "" {
			return g
		}
	}
	return graphical{}
}

func sessionProps(id string) map[string]string {
	out, err := exec.Command("loginctl", "show-session", id, "-p", "Active", "-p", "Display", "-p", "Name", "-p", "User").Output()
	if err != nil {
		return nil
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = strings.TrimSpace(value)
		}
	}
	return props
}

// xauthorityFor finds the session's X authority file, trying the places
// desktops keep it.
func xauthorityFor(props map[string]string) string {
	uid := props["User"]
	name := props["Name"]
	candidates := []string{
		"/run/user/" + uid + "/gdm/Xauthority",
		"/run/user/" + uid + "/.mutter-Xwaylandauth",
	}
	if home := homeOf(name); home != "" {
		candidates = append(candidates, filepath.Join(home, ".Xauthority"))
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// A glob for the ephemeral names GNOME on Wayland uses.
	if matches, _ := filepath.Glob("/run/user/" + uid + "/.mutter-Xwaylandauth.*"); len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func homeOf(name string) string {
	if name == "" {
		return ""
	}
	out, err := exec.Command("getent", "passwd", name).Output()
	if err != nil {
		return ""
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) >= 6 {
		return fields[5]
	}
	return ""
}
