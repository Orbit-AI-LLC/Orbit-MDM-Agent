//go:build !darwin

package service

// The menu-bar app is macOS-only; elsewhere these do nothing.

// InstallMenuApp is a no-op off macOS.
func InstallMenuApp(binaryPath, version string) error { return nil }

// InstallServiceBundle is a no-op off macOS, where the service isn't bundled.
func InstallServiceBundle(version string) error { return nil }

// InstallPackage is macOS-only (the .pkg update path); unused elsewhere.
func InstallPackage(pkg string) error { return nil }

// MenuAppVersion is empty off macOS, where there's no menu-bar app.
func MenuAppVersion() string { return "" }

func bootstrapMenuAgent() {}

func removeServiceBundle() {}
