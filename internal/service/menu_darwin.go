//go:build darwin

package service

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// The menu-bar app's bundle layout. The service (this binary, running as root)
// writes it when it installs or updates itself, so an agent that was enrolled
// before the menu-bar app existed and only ever updated its own binary still
// gets the icon. The .pkg lays the same files down from its payload for fresh
// installs (scripts/package_agent_macos.sh).
const menuExecPath = menuAppPath + "/Contents/MacOS/OrbitAgentMenu"
const menuInfoPath = menuAppPath + "/Contents/Info.plist"

// menuInfoPlist mirrors menubar/macos/Info.plist (%s is the version); keep them
// in step. The executable name must match CFBundleExecutable there.
func menuInfoPlist(version string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Orbit Agent</string>
  <key>CFBundleDisplayName</key><string>Orbit Agent</string>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleExecutable</key><string>OrbitAgentMenu</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHumanReadableCopyright</key><string>Orbit</string>
</dict>
</plist>
`, menuLabel, version, version)
}

// menuLaunchAgentPlist mirrors packaging/macos/ai.orbit.agent.menu.plist; keep
// them in step. The program path must match menuExecPath.
const menuLaunchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + menuLabel + `</string>
  <key>ProgramArguments</key>
  <array><string>` + menuExecPath + `</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>LimitLoadToSessionType</key><string>Aqua</string>
</dict>
</plist>
`

var menuVersionRE = regexp.MustCompile(`CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)

// MenuAppVersion is the version of the installed menu-bar app, or "" when it
// isn't installed (no executable, or no version in its Info.plist). The agent
// compares it with its own to decide whether the app needs (re)installing.
func MenuAppVersion() string {
	if _, err := os.Stat(menuExecPath); err != nil {
		return ""
	}
	data, err := os.ReadFile(menuInfoPath)
	if err != nil {
		return ""
	}
	if m := menuVersionRE.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// InstallMenuApp writes the menu-bar app bundle around the given universal
// binary, installs its per-user LaunchAgent, and starts it for whoever is signed
// in. It replaces whatever was there, so it both installs and updates.
func InstallMenuApp(binaryPath, version string) error {
	if err := os.MkdirAll(filepath.Dir(menuExecPath), 0o755); err != nil {
		return err
	}
	if err := copyFile(binaryPath, menuExecPath, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(menuInfoPath, []byte(menuInfoPlist(version)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(menuPlistPath, []byte(menuLaunchAgentPlist), 0o644); err != nil {
		return err
	}
	bootstrapMenuAgent()
	return nil
}

// bootstrapMenuAgent (re)loads the menu-bar app in the signed-in person's GUI
// session, so a new version shows up without a logout. It's a no-op at the login
// window; the LaunchAgent then loads it at the next login.
func bootstrapMenuAgent() {
	uid, ok := consoleUID()
	if !ok {
		return
	}
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+menuLabel).Run()
	_ = exec.Command("launchctl", "bootstrap", "gui/"+uid, menuPlistPath).Run()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
