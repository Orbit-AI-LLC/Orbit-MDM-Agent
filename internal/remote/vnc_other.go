//go:build !darwin

package remote

// builtInScreen has no implementation off macOS yet; callers fall back to a VNC
// server running on the computer.
func builtInScreen() (Screen, error) { return nil, ErrNoBuiltInScreen }
