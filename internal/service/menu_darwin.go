//go:build darwin

package service

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// orbitIcon is the Orbit logo both bundles show in System Settings and the
// Finder. It's the committed output of scripts/build_icon.sh (from the shared
// mark renderer scripts/orbitmark.swift); embedding it lets a self-updating
// agent keep the icon without a download. CFBundleIconFile points at
// "OrbitAgent", so it lands at Contents/Resources/OrbitAgent.icns.
//
//go:embed OrbitAgent.icns
var orbitIcon []byte

// iconName is the CFBundleIconFile value and the icns basename (no extension).
const iconName = "OrbitAgent"

// The root service's .app bundle. The service binary lives inside it (see
// config.BinaryPath, kept in step), so macOS lists it as "Orbit Agent" with the
// Orbit logo in Full Disk Access, Screen Recording and Accessibility rather than
// as a bare Unix tool. It has its own name so it never collides on disk with the
// menu-bar bundle below, which already owns "Orbit Agent.app". The .pkg lays the
// whole signed bundle down for installs and updates; copySelf (cmd/orbit-agent)
// places the binary, and InstallServiceBundle writes the metadata only when no
// bundle is there yet (a legacy bare-binary agent migrating in).
const serviceAppPath = "/Library/Orbit/Orbit Agent Service.app"
const serviceExecPath = serviceAppPath + "/Contents/MacOS/orbit-agent"
const serviceInfoPath = serviceAppPath + "/Contents/Info.plist"
const serviceIconPath = serviceAppPath + "/Contents/Resources/" + iconName + ".icns"

// legacyServiceBinary is where macOS kept the bare service binary before it moved
// into serviceAppPath; install and uninstall clear it off upgraded Macs.
const legacyServiceBinary = "/Library/Orbit/orbit-agent"

// The menu-bar app's bundle layout. The service (this binary, running as root)
// writes it when it installs or updates itself, so an agent that was enrolled
// before the menu-bar app existed and only ever updated its own binary still
// gets the icon. The .pkg lays the same files down from its payload for fresh
// installs (scripts/package_agent_macos.sh).
const menuExecPath = menuAppPath + "/Contents/MacOS/OrbitAgentMenu"
const menuInfoPath = menuAppPath + "/Contents/Info.plist"
const menuIconPath = menuAppPath + "/Contents/Resources/" + iconName + ".icns"

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
  <key>CFBundleIconFile</key><string>%s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHumanReadableCopyright</key><string>Orbit</string>
</dict>
</plist>
`, menuLabel, iconName, version, version)
}

// serviceInfoPlist is the Info.plist InstallServiceBundle writes when it builds a
// bundle for a legacy bare-binary agent; the signed .pkg ships
// packaging/macos/ServiceInfo.plist instead, and that copy — not this one — is
// what gets code-signed, so the two no longer need to match byte for byte. They
// should still describe the same bundle: CFBundleExecutable must match the binary
// copySelf places (orbit-agent),
// and CFBundleIdentifier must stay ai.orbit.agent, the identifier the PPPC
// profile's code requirement and permissions_darwin.go's TCC lookup key on.
// LSBackgroundOnly: it's a launchd daemon, not an app someone opens.
func serviceInfoPlist(version string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Orbit Agent</string>
  <key>CFBundleDisplayName</key><string>Orbit Agent</string>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleExecutable</key><string>orbit-agent</string>
  <key>CFBundleIconFile</key><string>%s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSBackgroundOnly</key><true/>
  <key>NSHumanReadableCopyright</key><string>Orbit</string>
</dict>
</plist>
`, label, iconName, version, version)
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
	if err := writeIcon(menuIconPath); err != nil {
		return err
	}
	if err := os.WriteFile(menuPlistPath, []byte(menuLaunchAgentPlist), 0o644); err != nil {
		return err
	}
	bootstrapMenuAgent()
	return nil
}

// InstallServiceBundle gives the root service's .app bundle its metadata — its
// Info.plist and icon — around the binary copySelf has placed inside it, so the
// privacy panes show "Orbit Agent" with the Orbit logo. It writes them only when
// the bundle isn't already there, and removes the bare binary a pre-bundle
// install left in /Library/Orbit.
//
// It must not touch an existing bundle. The signed .pkg lays the whole bundle
// down — Info.plist, icon and binary — for both fresh installs and updates, and
// its code signature seals those files; rewriting even one of them changes its
// bytes and breaks the seal, after which `codesign --verify` fails and macOS
// stops honouring the TCC privacy grants the PPPC profile ties to the bundle's
// code requirement (so Full Disk Access and the rest silently stop working). So
// we build the metadata ourselves only when there's no bundle yet — a legacy
// agent that enrolled as a bare binary, before the bundle existed, migrating
// into one — where there is no signature to protect.
func InstallServiceBundle(version string) error {
	// Only lay the metadata down when it's missing; never overwrite the signed
	// bundle the .pkg installed.
	if _, err := os.Stat(serviceInfoPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(serviceInfoPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(serviceInfoPath, []byte(serviceInfoPlist(version)), 0o644); err != nil {
			return err
		}
		if err := writeIcon(serviceIconPath); err != nil {
			return err
		}
	}
	// An upgrade from a pre-bundle agent leaves the old bare binary beside the
	// bundle; drop it so it isn't mistaken for the live one.
	_ = os.Remove(legacyServiceBinary)
	_ = os.Remove(legacyServiceBinary + ".old")
	return nil
}

// writeIcon lays the embedded Orbit icon down at path (Contents/Resources/…),
// creating the directory. Both bundles share the one icon.
func writeIcon(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, orbitIcon, 0o644)
}

// InstallPackage installs a downloaded .pkg with the system installer so that an
// update re-lays the whole bundle and reloads the launch daemon, the way a fresh
// install does. It's started detached (its own session) and the real work runs
// under installd, not this process, so the install finishes even when the
// package's postinstall reloads — and so replaces — this very daemon. It cleans
// up the package's temp directory when it's done.
// shellQuote wraps s so /bin/sh treats it as one literal argument, even if the
// path contains spaces or shell metacharacters.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func InstallPackage(pkg string) error {
	script := fmt.Sprintf(`/usr/sbin/installer -pkg %s -target / >>/var/log/orbit-agent-install.log 2>&1; /bin/rm -rf %s`,
		shellQuote(pkg), shellQuote(filepath.Dir(pkg)))
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// removeServiceBundle takes the root service's .app bundle off the computer on
// uninstall, along with any bare binary an older install left behind.
func removeServiceBundle() {
	_ = os.RemoveAll(serviceAppPath)
	_ = os.Remove(legacyServiceBinary)
	_ = os.Remove(legacyServiceBinary + ".old")
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

// The remote desktop's screen capture runs as a per-user LaunchAgent in the
// signed-in person's GUI session (not the root daemon), because macOS only lets a
// process in an active session be granted Screen Recording and raise its prompt —
// which is what lets the agent work without an MDM. It runs the same signed
// service binary with `desktop-helper`, so it keeps the ai.orbit.agent identity
// (the daemon dials it; see internal/remote/desktophelper_darwin.go). Its label
// and plist path are in service_unix.go, beside the menu agent's.
//
// desktopLaunchAgentPlist runs the service binary in desktop-helper mode in the
// GUI session (Aqua), restarted on failure. The program path is serviceExecPath.
const desktopLaunchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + desktopLabel + `</string>
  <key>ProgramArguments</key>
  <array><string>` + serviceExecPath + `</string><string>desktop-helper</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>LimitLoadToSessionType</key><string>Aqua</string>
</dict>
</plist>
`

// InstallDesktopAgent writes the desktop helper's LaunchAgent and loads it for
// whoever is signed in. Safe to run on every install; the helper is the same
// binary the service bundle already holds, so there's nothing else to lay down.
func InstallDesktopAgent() error {
	if err := os.WriteFile(desktopPlistPath, []byte(desktopLaunchAgentPlist), 0o644); err != nil {
		return err
	}
	bootstrapDesktopAgent()
	return nil
}

func bootstrapDesktopAgent() {
	uid, ok := consoleUID()
	if !ok {
		return
	}
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+desktopLabel).Run()
	_ = exec.Command("launchctl", "bootstrap", "gui/"+uid, desktopPlistPath).Run()
}

// bootoutDesktopAgent stops the desktop helper in the signed-in person's session,
// so it goes away with the rest of the agent on uninstall.
func bootoutDesktopAgent() {
	uid, ok := consoleUID()
	if !ok {
		return
	}
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+desktopLabel).Run()
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
