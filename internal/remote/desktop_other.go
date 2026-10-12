//go:build !linux

package remote

import (
	"context"
	"errors"
	"net"
)

// dialDesktop connects to a VNC server for the remote desktop. It prefers one
// already running on the computer (macOS Screen Sharing, or TightVNC/UltraVNC/
// TigerVNC on Windows); failing that, on platforms that support it (macOS) the
// agent serves its own built-in VNC server on an in-process pipe, so no screen
// sharing has to be turned on. Only the agent reaches it — nothing binds a port.
func dialDesktop(ctx context.Context, opt Options) (net.Conn, func(), error) {
	if conn := dialLocal(ctx, []int{5900, 5901, 5902}); conn != nil {
		return conn, func() {}, nil
	}
	// The per-user desktop helper, where screen capture runs in the signed-in
	// person's GUI session — the only place macOS lets Screen Recording be granted
	// without an MDM. Preferred over the daemon's own in-process capture below,
	// which recent macOS blocks for a process that isn't in an active session.
	if conn := dialDesktopHelper(ctx); conn != nil {
		return conn, func() {}, nil
	}
	screen, err := builtInScreen()
	if err == nil {
		client, server := net.Pipe()
		go func() { _ = serveVNC(ctx, server, screen) }() // serveVNC closes the screen when it ends
		return client, func() { server.Close() }, nil
	}
	if !errors.Is(err, ErrNoBuiltInScreen) {
		return nil, nil, err // e.g. the agent lacks Screen Recording permission — say so
	}
	return nil, nil, errors.New("No VNC server is answering on this computer. Install a VNC server (Windows: TightVNC, UltraVNC or TigerVNC) and try again.")
}
