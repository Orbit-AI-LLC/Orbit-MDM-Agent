//go:build !darwin

package service

// The menu-bar app is macOS-only; elsewhere these do nothing.

// InstallMenuApp is a no-op off macOS.
func InstallMenuApp(binaryPath, version string) error { return nil }

func bootstrapMenuAgent() {}
