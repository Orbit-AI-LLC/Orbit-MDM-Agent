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
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/config"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/pulse"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/remote"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/service"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/shell"
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

	watch       []string
	rebootSince int64
	queue       chan *tasks.Task
	exit        chan error
	nudge       chan struct{}
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
	resp, err := a.Client.Checkin(ctx, a.metrics(ctx))
	if err != nil {
		return err
	}
	a.watch = resp.WatchServices
	for _, task := range a.open(resp.Tasks) {
		a.run(ctx, task)
	}
	return nil
}

// Run checks in until the context ends or the server retires this computer.
func (a *Agent) Run(ctx context.Context) error {
	go a.worker(ctx)
	go a.pulse(ctx)
	backoff := 10 * time.Second
	for {
		interval := time.Duration(a.Config.CheckinSeconds) * time.Second
		resp, err := a.Client.Checkin(ctx, a.metrics(ctx))
		switch {
		case errors.Is(err, api.ErrRetired), errors.Is(err, api.ErrUnauthorized):
			return err
		case err != nil:
			a.Log.Printf("check-in failed: %v", err)
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
	return api.Result{ExitCode: code(0), Stdout: "Updated to " + version + "; restarting."}, func() error {
		err := service.Restart()
		a.exit <- errors.New("restarting after an update")
		return err
	}
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
