package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Restart restarts the computer in a minute, so the task's result goes back first.
func Restart(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		return exec.CommandContext(ctx, "shutdown.exe", "/r", "/t", "60", "/c", "Your IT team is restarting this computer with Orbit RMM.").Run()
	default:
		return exec.CommandContext(ctx, "shutdown", "-r", "+1").Run()
	}
}

// ShutDown turns the computer off in a minute.
func ShutDown(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		return exec.CommandContext(ctx, "shutdown.exe", "/s", "/t", "60", "/c", "Your IT team is shutting this computer down with Orbit RMM.").Run()
	default:
		return exec.CommandContext(ctx, "shutdown", "-h", "+1").Run()
	}
}

// Lock locks the screen of whoever is signed in.
func Lock(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		// The agent runs as SYSTEM in session 0; a one-off task runs as the
		// signed-in person, in their session, and locks it.
		script := `$u=(Get-CimInstance Win32_ComputerSystem).UserName; if(-not $u){throw 'Nobody is signed in.'}
schtasks /Create /TN OrbitLock /TR 'rundll32.exe user32.dll,LockWorkStation' /SC ONCE /ST 00:00 /RU $u /IT /F | Out-Null
schtasks /Run /TN OrbitLock | Out-Null; Start-Sleep 2; schtasks /Delete /TN OrbitLock /F | Out-Null`
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Run()
	case "darwin":
		return exec.CommandContext(ctx, "pmset", "displaysleepnow").Run()
	default:
		return exec.CommandContext(ctx, "loginctl", "lock-sessions").Run()
	}
}

// Download fetches a file and checks its SHA-256 before anything uses it.
func Download(ctx context.Context, url, sha string, dir string) (string, error) {
	if !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("only https downloads are allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the download answered %d", resp.StatusCode)
	}
	name := filepath.Base(req.URL.Path)
	if name == "" || name == "/" || name == "." {
		name = "download"
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(file, hash), resp.Body)
	file.Close()
	if err != nil {
		return "", err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, sha) {
		os.Remove(path)
		return "", fmt.Errorf("the download's SHA-256 is %s, not %s; not installing", got, sha)
	}
	return path, nil
}

// Checksum reads a SHA256SUMS file over https and returns the hex sum listed
// for name (lines are "<sum>  <name>"). It's how the agent finds the checksum of
// a sidecar asset, like the macOS menu-bar binary, that sits beside a build it's
// already been told to trust.
func Checksum(ctx context.Context, sumsURL, name string) (string, error) {
	if !strings.HasPrefix(sumsURL, "https://") {
		return "", fmt.Errorf("only https downloads are allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA256SUMS answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s isn't listed in SHA256SUMS", name)
}

// InstallPackage installs a downloaded package silently.
func InstallPackage(ctx context.Context, kind, path, arguments string) ([]byte, error) {
	extra := strings.Fields(arguments)
	var cmd *exec.Cmd
	switch kind {
	case "msi":
		cmd = exec.CommandContext(ctx, "msiexec.exe", append([]string{"/i", path, "/qn", "/norestart"}, extra...)...)
	case "exe":
		cmd = exec.CommandContext(ctx, path, extra...)
	case "pkg":
		cmd = exec.CommandContext(ctx, "installer", "-pkg", path, "-target", "/")
	case "deb":
		cmd = exec.CommandContext(ctx, "apt-get", "install", "-y", path)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	case "rpm":
		if _, err := exec.LookPath("dnf"); err == nil {
			cmd = exec.CommandContext(ctx, "dnf", "install", "-y", path)
		} else {
			cmd = exec.CommandContext(ctx, "zypper", "-n", "install", path)
		}
	default:
		return nil, fmt.Errorf("unknown package kind %q", kind)
	}
	return cmd.CombinedOutput()
}

// Service starts, stops or restarts a service.
func Service(ctx context.Context, name, action string) ([]byte, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return nil, fmt.Errorf("unknown action %q", action)
	}
	switch runtime.GOOS {
	case "windows":
		verb := map[string]string{"start": "Start-Service", "stop": "Stop-Service", "restart": "Restart-Service"}[action]
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", verb+" -Name '"+strings.ReplaceAll(name, "'", "")+"' -Force").CombinedOutput()
	case "darwin":
		if action == "stop" {
			return exec.CommandContext(ctx, "launchctl", "kill", "TERM", "system/"+name).CombinedOutput()
		}
		return exec.CommandContext(ctx, "launchctl", "kickstart", "-k", "system/"+name).CombinedOutput()
	default:
		return exec.CommandContext(ctx, "systemctl", action, name).CombinedOutput()
	}
}
