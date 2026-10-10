// Command orbit-agent is the Orbit RMM agent.
//
//	orbit-agent install --server https://mdm.example.com --token orbe_…
//	orbit-agent run          (what the service runs)
//	orbit-agent once         (check in and run what comes back, then exit)
//	orbit-agent status
//	orbit-agent uninstall
//	orbit-agent version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/agent"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/config"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/service"
	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/system"
)

// Version is set at build time: -ldflags "-X main.Version=1.2.3".
var Version = "1.0.0"

func main() {
	logger := log.New(os.Stderr, "orbit-agent: ", log.LstdFlags)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = install(os.Args[2:], logger)
	case "run":
		err = run(logger)
	case "once":
		err = once(logger)
	case "status":
		err = status()
	case "uninstall":
		err = agent.Remove()
		if err == nil {
			fmt.Println("The Orbit agent has been removed.")
		}
	case "version":
		fmt.Printf("orbit-agent %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: orbit-agent <command>
  install --server URL --token TOKEN [--no-service]   enroll this computer and start the service
  install --package [--server URL --token TOKEN]      what the .pkg and .msi run: see below
  run                                                 run the agent (the service does this)
  once                                                check in once and run what comes back
  status                                              show the enrollment
  uninstall                                           remove the agent
  version                                             print the version

With --package (the .pkg's postinstall, the .msi's custom action) an agent
that is already enrolled keeps its enrollment and only runs the new binary.
Otherwise it enrolls with --server and --token, or with the Server and Token
an MDM set (macOS: managed preferences for ai.orbit.agent; Windows:
HKLM\SOFTWARE\Policies\Orbit\Agent); with neither it stays installed
and unenrolled, and says so.`)
}

func install(args []string, logger *log.Logger) error {
	flags := flag.NewFlagSet("install", flag.ExitOnError)
	server := flags.String("server", "", "Orbit MDM's address, https://…")
	token := flags.String("token", "", "the install token from Orbit RMM")
	noService := flags.Bool("no-service", false, "enroll only; don't copy the binary or install the service")
	fromPackage := flags.Bool("package", false, "run by an installer package: keep an existing enrollment, or enroll with what an MDM set")
	_ = flags.Parse(args)
	if *fromPackage {
		if _, err := config.Load(); err == nil {
			// An upgrade: never enroll again (a token may be single-use, and
			// moving organizations means uninstalling first).
			fmt.Println("The Orbit agent is already enrolled; starting the new version.")
			return startService(*noService)
		}
		if *server == "" || *token == "" {
			*server, *token = managedEnrollment()
		}
		if *server == "" || *token == "" {
			fmt.Println("The Orbit agent is installed but not enrolled. Run orbit-agent install --server … --token …, or have your MDM set Server and Token for it.")
			return nil
		}
	}
	if *server == "" || *token == "" {
		return errors.New("--server and --token are required (copy the install command from Orbit RMM)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	who := system.Who(ctx)
	client := api.New(*server, "", Version)
	enrolled, err := client.Enroll(ctx, api.EnrollRequest{
		Token: *token, Hostname: who.Hostname, OS: who.OS, OSVersion: who.OSVersion, Arch: who.Arch, Serial: who.Serial,
		MachineID: who.MachineID, Manufacturer: who.Manufacturer, Model: who.Model, Version: Version,
	})
	if err != nil {
		return fmt.Errorf("enrollment failed: %w", err)
	}
	cfg := &config.Config{Server: client.Server, AgentID: enrolled.AgentID, Secret: enrolled.Secret, ServerKey: enrolled.ServerKey,
		CheckinSeconds: enrolled.CheckinSeconds, Device: enrolled.Device, PulseAddr: enrolled.PulseAddr}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Enrolled %s with Orbit RMM.\n", who.Hostname)
	return startService(*noService)
}

// startService puts this binary in place and (re)starts the service.
func startService(noService bool) error {
	if noService {
		return nil
	}
	binary, err := copySelf()
	if err != nil {
		return err
	}
	// On macOS the binary sits inside an .app bundle; give the bundle its name
	// and icon (a no-op elsewhere) so the privacy panes show "Orbit Agent".
	if err := service.InstallServiceBundle(Version); err != nil {
		return fmt.Errorf("couldn't write the service bundle: %w", err)
	}
	if err := service.Install(binary); err != nil {
		return fmt.Errorf("couldn't install the service: %w", err)
	}
	fmt.Println("The Orbit agent is running.")
	return nil
}

// copySelf puts this binary where the service runs it from.
func copySelf() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	target := config.BinaryPath()
	if same, _ := filepath.Abs(self); same == target {
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := target + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	return target, os.Rename(tmp, target)
}

func load(logger *log.Logger) (*agent.Agent, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return agent.New(cfg, Version, logger), nil
}

func run(logger *log.Logger) error {
	a, err := load(logger)
	if err != nil {
		return err
	}
	work := func(ctx context.Context) {
		if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Print(err)
		}
	}
	if ran, err := service.RunAsService(work); ran || err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = a.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func once(logger *log.Logger) error {
	a, err := load(logger)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return a.Once(ctx)
}

func status() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("Server:   %s\nAgent:    %s\nDevice:   %s\nVersion:  %s\nCheck-in: every %ds\nConfig:   %s\n",
		cfg.Server, cfg.AgentID, cfg.Device, Version, cfg.CheckinSeconds, config.Path())
	return nil
}
