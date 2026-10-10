// Package agent is the Orbit agent's work: check in, verify tasks, run them,
// report back.
package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/config"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/pulse"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/remote"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/service"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/shell"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/status"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/system"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/tasks"
)

// Agent runs as one enrolled computer.
type Agent struct {
	Config  *config.Config
	Client  *api.Client
	Seen    *tasks.Seen
	Log     *log.Logger
	Version string
	// Metrics reads health for a check-in; tests replace it.
	Metrics func(ctx context.Context, watch []string) map[string]any

	watch           []string
	rebootSince     int64
	lastGoodCheckin string
	queue           chan *tasks.Task
	exit            chan error
	nudge           chan struct{}
}

// New makes an agent from its configuration.
func New(cfg *config.Config, version string, logger *log.Logger) *Agent {
	return &Agent{
		Config:  cfg,
		Client:  api.New(cfg.Server, cfg.Secret, version),
		Seen:    tasks.LoadSeen(config.StateDir()),
		Log:     logger,
		Version: version,
		Metrics: system.Metrics,
		queue:   make(chan *tasks.Task, 64),
		exit:    make(chan error, 1),
		nudge:   make(chan struct{}, 1),
	}
}

func (a *Agent) metrics(ctx context.Context) map[string]any {
	m := a.Metrics(ctx, a.watch)
	if pending, _ := m["reboot_pending"].(bool); pending {
		if a.rebootSince == 0 {
			a.rebootSince = time.Now().Unix()
		}
		m["reboot_pending_since"] = a.rebootSince
	} else {
		a.rebootSince = 0
	}
	return m
}

// Once checks in and runs whatever comes back, then returns.
func (a *Agent) Once(ctx context.Context) error {
	m := a.metrics(ctx)
	resp, err := a.Client.Checkin(ctx, m)
	if err != nil {
		a.writeStatus(false, m)
		return err
	}
	a.watch = resp.WatchServices
	a.applySupport(resp.Support)
	a.writeStatus(true, m)
	for _, task := range a.open(resp.Tasks) {
		a.run(ctx, task)
	}
	return nil
}

// Run checks in until the context ends or the server retires this computer.
func (a *Agent) Run(ctx context.Context) error {
	go a.worker(ctx)
	go a.pulse(ctx)
	go a.ensureMenuApp(ctx)
	backoff := 10 * time.Second
	for {
		interval := time.Duration(a.Config.CheckinSeconds) * time.Second
		m := a.metrics(ctx)
		resp, err := a.Client.Checkin(ctx, m)
		switch {
		case errors.Is(err, api.ErrRetired), errors.Is(err, api.ErrUnauthorized):
			return err
		case err != nil:
			a.Log.Printf("check-in failed: %v", err)
			a.writeStatus(false, m)
			interval = backoff
			if backoff < 5*time.Minute {
				backoff *= 2
			}
		default:
			backoff = 10 * time.Second
			a.watch = resp.WatchServices
			if resp.CheckinSeconds > 0 {
				interval = time.Duration(resp.CheckinSeconds) * time.Second
			}
			a.rememberPulseAddr(resp.PulseAddr)
			a.applySupport(resp.Support)
			a.writeStatus(true, m)
			a.enqueue(resp.Tasks)
			live := resp.Live
			for live && ctx.Err() == nil {
				waited, err := a.Client.Wait(ctx)
				if err != nil {
					break
				}
				a.enqueue(waited.Tasks)
				live = waited.Live
			}
		}
		jitter := time.Duration(rand.Int63n(int64(interval)/5+1)) - interval/10
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-a.exit:
			return err
		case <-a.nudge:
			// The pulse channel said there's work; check in now.
		case <-time.After(interval + jitter):
		}
	}
}

// pulse keeps the connection to the server open so a nudge reaches the agent at
// once (internal/pulse). It needs the server's address, learned from a
// check-in; without one it does nothing.
func (a *Agent) pulse(ctx context.Context) {
	for a.Config.PulseAddr == "" {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
	signer, err := a.Config.PulseKey()
	if err != nil {
		a.Log.Printf("pulse: couldn't make a key: %v", err)
		return
	}
	public := base64.StdEncoding.EncodeToString(signer.Public().(ed25519.PublicKey))
	for ctx.Err() == nil {
		if err := a.Client.RegisterPulseKey(ctx, public); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
	serverKey, err := base64.StdEncoding.DecodeString(a.Config.ServerKey)
	if err != nil || len(serverKey) != ed25519.PublicKeySize {
		a.Log.Printf("pulse: the pinned server key is unusable")
		return
	}
	pulse.Maintain(ctx, pulse.Options{
		Addr: a.Config.PulseAddr, AgentID: a.Config.AgentID,
		ServerKey: ed25519.PublicKey(serverKey), Signer: signer,
		OnNudge: a.wake, Logger: a.Log,
	})
}

func (a *Agent) wake() {
	select {
	case a.nudge <- struct{}{}:
	default:
	}
}

// rememberPulseAddr saves the pulse address the server sent, if it changed, so a
// restart picks it up (the pulse loop reads it once at start).
func (a *Agent) rememberPulseAddr(addr string) {
	if addr == a.Config.PulseAddr {
		return
	}
	a.Config.PulseAddr = addr
	if err := a.Config.Save(); err != nil {
		a.Log.Printf("pulse: couldn't save the address: %v", err)
	}
}

// applySupport remembers a help-desk contact the server sent, saving it so the
// tray still has it after a restart, before the first check-in.
func (a *Agent) applySupport(s *status.Support) {
	if s == nil || *s == a.Config.Support {
		return
	}
	a.Config.Support = *s
	if err := a.Config.Save(); err != nil {
		a.Log.Printf("couldn't save the help-desk contact: %v", err)
	}
}

// writeStatus refreshes the world-readable file the tray reads, from the last
// check-in's result and metrics (internal/status).
func (a *Agent) writeStatus(ok bool, m map[string]any) {
	host, _ := os.Hostname()
	now := time.Now().UTC().Format(time.RFC3339)
	if ok {
		a.lastGoodCheckin = now
	}
	s := status.Status{
		Version:       a.Version,
		Hostname:      host,
		OS:            runtime.GOOS,
		Enrolled:      true,
		Server:        a.Config.Server,
		Device:        a.Config.Device,
		LastCheckin:   a.lastGoodCheckin,
		LastCheckinOK: ok,
		Support:       a.Config.Support,
		UpdatedAt:     now,
	}
	if perms, _ := m["permissions"].(map[string]string); len(perms) > 0 {
		s.Permissions = perms
	}
	s.Issues = issues(ok, m, s.Permissions)
	s.Healthy = healthy(s.Issues)
	if err := status.Write(config.StatusPath(), s); err != nil {
		a.Log.Printf("couldn't write the status file: %v", err)
	}
}

// issues turns the last check-in into the problems the tray shows, worst first.
func issues(ok bool, m map[string]any, perms map[string]string) []status.Issue {
	var out []status.Issue
	if !ok {
		out = append(out, status.Issue{ID: "checkin", Severity: status.Error,
			Title: "Can't reach Orbit RMM", Detail: "The agent couldn't check in with the server."})
	}
	switch perms["screen_recording"] {
	case status.Denied:
		out = append(out, status.Issue{ID: "screen_recording", Severity: status.Error,
			Title:  "Screen Recording is off",
			Detail: "Remote support can't see this screen until Screen Recording is allowed for the Orbit agent.",
			Fix:    "open_screen_recording"})
	}
	if perms["full_disk_access"] == status.Denied {
		out = append(out, status.Issue{ID: "full_disk_access", Severity: status.Warning,
			Title:  "Full Disk Access is off",
			Detail: "Some inventory and management needs Full Disk Access for the Orbit agent.",
			Fix:    "open_full_disk_access"})
	}
	if pending, _ := m["reboot_pending"].(bool); pending {
		out = append(out, status.Issue{ID: "reboot", Severity: status.Warning,
			Title: "Restart pending", Detail: "Updates need a restart to finish."})
	}
	if perms["accessibility"] == status.Denied {
		out = append(out, status.Issue{ID: "accessibility", Severity: status.Info,
			Title:  "Accessibility is off",
			Detail: "Needed later for remote keyboard and mouse control.",
			Fix:    "open_accessibility"})
	}
	return out
}

func healthy(issues []status.Issue) bool {
	for _, i := range issues {
		if i.Severity == status.Error {
			return false
		}
	}
	return true
}

func (a *Agent) open(sealed []api.Sealed) []*tasks.Task {
	var out []*tasks.Task
	for _, s := range sealed {
		task, err := tasks.Open(s, a.Config.ServerKey, a.Config.AgentID, time.Now(), a.Seen)
		if err != nil {
			a.Log.Printf("refused task %s: %v", s.ID, err)
			continue
		}
		out = append(out, task)
	}
	return out
}

func (a *Agent) enqueue(sealed []api.Sealed) {
	for _, task := range a.open(sealed) {
		select {
		case a.queue <- task:
		default:
			a.Log.Printf("task %s dropped: too much queued", task.ID)
		}
	}
}

func (a *Agent) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-a.queue:
			a.run(ctx, task)
		}
	}
}

// run records the task as seen (so it can never run twice), runs it,
// reports the result, and only then does anything that ends this process.
func (a *Agent) run(ctx context.Context, task *tasks.Task) {
	if err := a.Seen.Add(task.ID, time.Now()); err != nil {
		a.Log.Printf("could not record task %s: %v", task.ID, err)
		return
	}
	_ = a.Client.Start(ctx, task.ID)
	result, after := a.execute(ctx, task)
	if err := a.Client.Report(ctx, task.ID, result); err != nil {
		a.Log.Printf("could not report task %s: %v", task.ID, err)
	}
	if after != nil {
		if err := after(); err != nil {
			a.Log.Printf("after task %s: %v", task.ID, err)
		}
	}
}

func code(n int) *int { return &n }

func failed(err error) api.Result {
	return api.Result{ExitCode: code(1), Error: err.Error()}
}

func (a *Agent) execute(ctx context.Context, task *tasks.Task) (api.Result, func() error) {
	var p map[string]any
	_ = json.Unmarshal(task.Payload, &p)
	str := func(k string) string { s, _ := p[k].(string); return s }
	switch task.Kind {
	case "script":
		timeout, _ := p["timeout"].(float64)
		out, err := shell.Run(ctx, shell.Request{Shell: str("shell"), Body: str("body"), Timeout: time.Duration(timeout) * time.Second, RunAs: str("run_as")})
		if err != nil {
			return failed(err), nil
		}
		return api.Result{ExitCode: code(out.ExitCode), Stdout: out.Stdout, Stderr: out.Stderr}, nil
	case "inventory":
		return api.Result{ExitCode: code(0), Data: system.Inventory(ctx)}, nil
	case "patch_scan":
		updates, err := system.ScanUpdates(ctx)
		if err != nil {
			return failed(err), nil
		}
		return api.Result{ExitCode: code(0), Stdout: fmt.Sprintf("%d updates available.", len(updates)), Data: map[string]any{"updates": updates}}, nil
	case "patch_install":
		var ids []string
		if raw, ok := p["ids"].([]any); ok {
			for _, id := range raw {
				if s, ok := id.(string); ok {
					ids = append(ids, s)
				}
			}
		}
		results, log, err := system.InstallUpdates(ctx, ids)
		result := api.Result{ExitCode: code(0), Stdout: log, Data: map[string]any{"results": results}}
		if err != nil {
			result.ExitCode, result.Error = code(1), err.Error()
		}
		reboot := str("reboot")
		if reboot == "always" || (reboot == "if_needed" && rebootPending(ctx, a)) {
			return result, func() error { return system.Restart(context.Background()) }
		}
		return result, nil
	case "software_install":
		dir, err := os.MkdirTemp("", "orbit-install-")
		if err != nil {
			return failed(err), nil
		}
		defer os.RemoveAll(dir)
		path, err := system.Download(ctx, str("url"), str("sha256"), dir)
		if err != nil {
			return failed(err), nil
		}
		out, err := system.InstallPackage(ctx, str("kind"), path, str("arguments"))
		if err != nil {
			return api.Result{ExitCode: code(1), Stdout: string(out), Error: err.Error()}, nil
		}
		return api.Result{ExitCode: code(0), Stdout: string(out)}, nil
	case "reboot":
		return api.Result{ExitCode: code(0), Stdout: "Restarting in a minute."}, func() error { return system.Restart(context.Background()) }
	case "shutdown":
		return api.Result{ExitCode: code(0), Stdout: "Shutting down in a minute."}, func() error { return system.ShutDown(context.Background()) }
	case "lock":
		if err := system.Lock(ctx); err != nil {
			return failed(err), nil
		}
		return api.Result{ExitCode: code(0), Stdout: "Locked."}, nil
	case "service":
		out, err := system.Service(ctx, str("name"), str("action"))
		if err != nil {
			return api.Result{ExitCode: code(1), Stdout: string(out), Error: err.Error()}, nil
		}
		return api.Result{ExitCode: code(0), Stdout: string(out)}, nil
	case "remote":
		return a.remote(ctx, str("session"), str("mode"), str("shell"), str("relay"), str("by"))
	case "update_agent":
		return a.update(ctx, str("url"), str("sha256"), str("version"))
	case "uninstall":
		return api.Result{ExitCode: code(0), Stdout: "Removing the Orbit agent."}, a.uninstall
	}
	return failed(fmt.Errorf("unknown task kind %q", task.Kind)), nil
}

func rebootPending(ctx context.Context, a *Agent) bool {
	pending, _ := a.Metrics(ctx, nil)["reboot_pending"].(bool)
	return pending
}

// remote joins a live terminal or remote desktop in the background, so the
// worker is free for other tasks while the session, which may last a long
// time, runs. The relay ends it (idle, too long, or an admin's choice).
func (a *Agent) remote(ctx context.Context, session, mode, shell, relay, by string) (api.Result, func() error) {
	if relay == "" || mode == "" {
		return failed(errors.New("the remote task didn't say where to connect")), nil
	}
	opt := remote.Options{
		Session: session, Mode: mode, Shell: shell, Relay: relay, By: by,
		Secret: a.Config.Secret, Version: a.Version, Logger: a.Log,
	}
	go func() {
		if err := remote.Run(ctx, opt); err != nil {
			a.Log.Printf("remote %s session %s ended: %v", mode, session, err)
		}
	}()
	return api.Result{ExitCode: code(0), Stdout: "Joined the " + mode + " session."}, nil
}

// update replaces this binary with a newer one and has the service manager restart it.
func (a *Agent) update(ctx context.Context, url, sha, version string) (api.Result, func() error) {
	current, err := os.Executable()
	if err != nil {
		return failed(err), nil
	}
	dir := filepath.Dir(current)
	path, err := system.Download(ctx, url, sha, dir)
	if err != nil {
		return failed(err), nil
	}
	old := current + ".old"
	_ = os.Remove(old)
	if err := os.Rename(current, old); err != nil {
		os.Remove(path)
		return failed(err), nil
	}
	if err := os.Rename(path, current); err != nil {
		_ = os.Rename(old, current)
		return failed(err), nil
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(current, 0o755)
	}
	// Keep the macOS menu-bar app in step. Best effort: an agent that updated
	// only its binary (enrolled before the app existed, or on a locked-down
	// network) stays updated even if the icon can't be refreshed.
	if runtime.GOOS == "darwin" {
		if err := a.refreshMenuApp(ctx, url, version); err != nil {
			a.Log.Printf("couldn't refresh the menu-bar app: %v", err)
		}
	}
	return api.Result{ExitCode: code(0), Stdout: "Updated to " + version + "; restarting."}, func() error {
		err := service.Restart()
		a.exit <- errors.New("restarting after an update")
		return err
	}
}

// releaseDownloadBase is where a release publishes its assets; the menu-bar
// binary (orbit-agent-menu-darwin) sits beside the agent binary under
// agent-v<version>/. The agent reads it to heal the menu-bar app for its own
// version (ensureMenuApp) and after an update (refreshMenuApp). The repository is
// public so this stays reachable, the same place the server points updates at.
const releaseDownloadBase = "https://github.com/Orbit-AI-LLC/Orbit-MDM-Agent/releases/download"

// ensureMenuApp installs the macOS menu-bar app for the version now running when
// it's missing or out of date. The service updates itself by replacing only its
// binary, so without this an agent enrolled before the app existed, or one that
// updated binary-only, never gets the icon until a later update. It runs at
// startup; a no-op (and no network) when the app already matches, so it costs
// nothing on a healthy computer. Best effort: a failure only means no icon yet.
func (a *Agent) ensureMenuApp(ctx context.Context) {
	if runtime.GOOS != "darwin" || service.MenuAppVersion() == a.Version {
		return
	}
	if err := a.installMenuApp(ctx, releaseDownloadBase+"/agent-v"+a.Version, a.Version); err != nil {
		a.Log.Printf("couldn't install the menu-bar app: %v", err)
	}
}

// refreshMenuApp reinstalls the menu-bar app for the version just updated to,
// from the same release the new binary came from.
func (a *Agent) refreshMenuApp(ctx context.Context, binURL, version string) error {
	u, err := url.Parse(binURL)
	if err != nil {
		return err
	}
	dir := *u
	dir.Path = path.Dir(u.Path)
	return a.installMenuApp(ctx, dir.String(), version)
}

// installMenuApp downloads the menu-bar binary from a release (base is that
// release's download URL), checks it against the release's SHA256SUMS, and has
// the service write the app bundle and LaunchAgent around it (internal/service).
func (a *Agent) installMenuApp(ctx context.Context, base, version string) error {
	sha, err := system.Checksum(ctx, base+"/SHA256SUMS", "orbit-agent-menu-darwin")
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "orbit-menu-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	menuBin, err := system.Download(ctx, base+"/orbit-agent-menu-darwin", sha, tmp)
	if err != nil {
		return err
	}
	return service.InstallMenuApp(menuBin, version)
}

// uninstall removes the service, the configuration and the binary.
func (a *Agent) uninstall() error {
	err := Remove()
	a.exit <- api.ErrRetired
	return err
}

// Remove takes the agent off this computer.
func Remove() error {
	err := service.Uninstall()
	_ = os.RemoveAll(config.Dir())
	if config.StateDir() != config.Dir() {
		_ = os.RemoveAll(config.StateDir())
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(config.BinaryPath())
	}
	return err
}
