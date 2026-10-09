//go:build windows

// Package service installs the agent so the system keeps it running:
// a systemd unit on Linux, a launch daemon on macOS, a service on Windows.
package service

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Name is the service's name.
const Name = "OrbitAgent"

// Install registers the service (automatic start, restarted when it fails) and starts it.
func Install(binary string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if existing, err := m.OpenService(Name); err == nil {
		_, _ = existing.Control(svc.Stop)
		time.Sleep(2 * time.Second)
		_ = existing.Delete()
		existing.Close()
		time.Sleep(2 * time.Second)
	}
	s, err := m.CreateService(Name, binary, mgr.Config{
		DisplayName: "Orbit RMM agent",
		Description: "Lets your organization monitor, update and support this computer with Orbit RMM.",
		StartType:   mgr.StartAutomatic,
	}, "run")
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
	return s.Start()
}

// Uninstall stops the service and removes it.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	return s.Delete()
}

// Restart has the service manager start the agent again, from a separate
// process because this one is about to stop.
func Restart() error {
	return exec.Command("cmd.exe", "/c", fmt.Sprintf("timeout /t 3 >nul & sc.exe stop %s & timeout /t 5 >nul & sc.exe start %s", Name, Name)).Start()
}

type handler struct {
	run func(ctx context.Context)
}

func (h handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.run(ctx)
		close(done)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		case <-done:
			return false, 0
		}
	}
}

// RunAsService runs the agent under the service manager when it started it.
func RunAsService(run func(ctx context.Context)) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	return true, svc.Run(Name, handler{run: run})
}
