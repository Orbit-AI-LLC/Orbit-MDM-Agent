//go:build !linux

package remote

import (
	"context"
	"errors"
	"net"
)

// dialDesktop finds the VNC server on a Mac or a Windows PC: the built-in
// Screen Sharing on macOS, or TightVNC/UltraVNC/TigerVNC on Windows, both on
// the usual ports. Nothing is started; the person turns sharing on.
func dialDesktop(ctx context.Context, opt Options) (net.Conn, func(), error) {
	if conn := dialLocal(ctx, []int{5900, 5901, 5902}); conn != nil {
		return conn, func() {}, nil
	}
	return nil, nil, errors.New("No VNC server is answering on this computer. Turn on screen sharing (macOS: System Settings, General, Sharing; Windows: install TightVNC, UltraVNC or TigerVNC) and try again.")
}
