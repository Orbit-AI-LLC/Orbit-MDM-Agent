//go:build !windows

// Package service installs the agent so the system keeps it running:
// a systemd unit on Linux, a launch daemon on macOS, a service on Windows.
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Name is the service's name.
const Name = "orbit-agent"

const label = "ai.orbit.agent"
const plistPath = "/Library/LaunchDaemons/" + label + ".plist"
const unitPath = "/etc/systemd/system/orbit-agent.service"

// The macOS menu-bar app runs as a per-user LaunchAgent (packaging/macos).
const menuLabel = "ai.orbit.agent.menu"
const menuPlistPath = "/Library/LaunchAgents/" + menuLabel + ".plist"
const menuAppPath = "/Library/Orbit/Orbit Agent.app"

func unit(binary string) string {
	return fmt.Sprintf(`[Unit]
Description=Orbit RMM agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s run
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`, binary)
}

func plist(binary string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/orbit-agent.log</string>
  <key>StandardErrorPath</key><string>/var/log/orbit-agent.log</string>
</dict>
</plist>
`, label, binary)
}

func sh(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v: %s", name, args, err, out)
	}
	return nil
}

// Install registers and starts the service.
func Install(binary string) error {
	if runtime.GOOS == "darwin" {
		if err := os.WriteFile(plistPath, []byte(plist(binary)), 0o644); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
		if err := sh("launchctl", "bootstrap", "system", plistPath); err != nil {
			return err
		}
		// When a package already laid the menu-bar app down, make sure it's
		// running for whoever is signed in (fresh installs do this in the
		// postinstall too); self-updates write it in agent.update.
		if _, err := os.Stat(menuAppPath); err == nil {
			bootstrapMenuAgent()
		}
		return nil
	}
	if err := os.WriteFile(unitPath, []byte(unit(binary)), 0o644); err != nil {
		return err
	}
	if err := sh("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return sh("systemctl", "enable", "--now", "orbit-agent")
}

// consoleUID is the uid of whoever is signed in at the Mac's screen, or "", false
// when that's nobody (the login window, where the user is "root"). The menu-bar
// app is a per-user LaunchAgent, so loading or unloading it needs this uid.
func consoleUID() (string, bool) {
	out, err := exec.Command("stat", "-f%Su", "/dev/console").Output()
	if err != nil {
		return "", false
	}
	user := strings.TrimSpace(string(out))
	if user == "" || user == "root" {
		return "", false
	}
	uid, err := exec.Command("id", "-u", user).Output()
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(uid))
	if id == "" {
		return "", false
	}
	return id, true
}

// bootoutMenuAgent stops the menu-bar app in the signed-in person's session, so
// it goes away with the rest of the agent (macOS).
func bootoutMenuAgent() {
	uid, ok := consoleUID()
	if !ok {
		return
	}
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+menuLabel).Run()
}

// Uninstall stops the service and removes it.
func Uninstall() error {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
		bootoutMenuAgent()
		_ = os.Remove(menuPlistPath)
		_ = os.RemoveAll(menuAppPath)
		removeServiceBundle()
		_ = os.Remove("/Library/Orbit/status.json")
		return os.Remove(plistPath)
	}
	_ = exec.Command("systemctl", "disable", "--now", "orbit-agent").Run()
	err := os.Remove(unitPath)
	_ = exec.Command("systemctl", "daemon-reload").Run()
	return err
}

// Restart asks the system to start the agent again (after it replaced itself).
func Restart() error {
	if runtime.GOOS == "darwin" {
		return sh("launchctl", "kickstart", "-k", "system/"+label)
	}
	return sh("systemctl", "restart", "orbit-agent")
}

// RunAsService is for Windows; elsewhere the system runs `orbit-agent run` directly.
func RunAsService(run func(ctx context.Context)) (bool, error) { return false, nil }
