//go:build !darwin

package remote

import (
	"context"
	"errors"
	"log"
	"net"
)

// The user-session desktop helper is macOS-only (it exists to get Screen
// Recording granted without an MDM). Elsewhere there's nothing to run or dial.

// RunDesktopHelper is a no-op off macOS.
func RunDesktopHelper(ctx context.Context, logger *log.Logger) error {
	return errors.New("the desktop helper is only used on macOS")
}

// dialDesktopHelper never finds a helper off macOS.
func dialDesktopHelper(ctx context.Context) net.Conn { return nil }
