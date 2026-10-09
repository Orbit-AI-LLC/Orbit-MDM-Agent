// Package config is where the agent keeps who it is and whom it trusts.
//
// The file holds the server's address, the agent's id and secret, and the
// server's Ed25519 public key, pinned at enrollment: every task must be signed
// with it. It is readable by root (or SYSTEM) alone.
package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/status"
)

// Config is the agent's enrollment.
type Config struct {
	Server         string `json:"server"`
	AgentID        string `json:"agent_id"`
	Secret         string `json:"secret"`
	ServerKey      string `json:"server_key"`
	CheckinSeconds int    `json:"checkin_seconds"`
	Device         string `json:"device"`
	// PulseAddr is where the agent keeps a connection open (host:port); the
	// server sends it, and blank means the channel is off.
	PulseAddr string `json:"pulse_addr,omitempty"`
	// PulseSeed is base64 of this agent's 32-byte Ed25519 seed for the pulse
	// channel, made on first use; the public half is registered with the server.
	PulseSeed string `json:"pulse_seed,omitempty"`
	// Support is the organization's help-desk contact, last sent by the server;
	// kept so the tray can show it even before the first check-in after a start.
	Support status.Support `json:"support,omitempty"`
}

// ErrNotEnrolled means there is no configuration yet.
var ErrNotEnrolled = errors.New("this computer isn't enrolled; run orbit-agent install")

// Dir is where the configuration and the agent's state live.
// ORBIT_AGENT_HOME overrides it (for tests and side-by-side installs).
func Dir() string {
	if home := os.Getenv("ORBIT_AGENT_HOME"); home != "" {
		return home
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Orbit", "Agent")
	case "darwin":
		return "/Library/Application Support/Orbit Agent"
	default:
		return "/etc/orbit-agent"
	}
}

// StateDir holds what changes as the agent runs (tasks already run).
func StateDir() string {
	if home := os.Getenv("ORBIT_AGENT_HOME"); home != "" {
		return home
	}
	if runtime.GOOS == "linux" {
		return "/var/lib/orbit-agent"
	}
	return Dir()
}

// BinaryPath is where install puts the agent itself.
func BinaryPath() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("ProgramFiles")
		if base == "" {
			base = `C:\Program Files`
		}
		return filepath.Join(base, "Orbit", "Agent", "orbit-agent.exe")
	case "darwin":
		return "/Library/Orbit/orbit-agent"
	default:
		return "/usr/local/bin/orbit-agent"
	}
}

// Path is the configuration file.
func Path() string { return filepath.Join(Dir(), "config.json") }

// PublicDir is a place anyone signed in can read, where the service writes the
// tray's status file (unlike Dir, which only root or SYSTEM can read). On macOS
// it's /Library/Orbit, beside the binary; on Windows the ProgramData folder,
// which users can read; on Linux the state directory.
func PublicDir() string {
	if home := os.Getenv("ORBIT_AGENT_HOME"); home != "" {
		return home
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Orbit"
	case "windows":
		return Dir()
	default:
		return StateDir()
	}
}

// StatusPath is the world-readable status file the tray reads (internal/status).
func StatusPath() string { return filepath.Join(PublicDir(), "status.json") }

// PulseKey returns this agent's Ed25519 key for the pulse channel, making and
// saving one on first use. The caller registers the public half with the server.
func (c *Config) PulseKey() (ed25519.PrivateKey, error) {
	if c.PulseSeed != "" {
		if seed, err := base64.StdEncoding.DecodeString(c.PulseSeed); err == nil && len(seed) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(seed), nil
		}
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	c.PulseSeed = base64.StdEncoding.EncodeToString(priv.Seed())
	if err := c.Save(); err != nil {
		return nil, err
	}
	return priv, nil
}

// Load reads the configuration.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Server == "" || c.Secret == "" || c.ServerKey == "" {
		return nil, ErrNotEnrolled
	}
	if c.CheckinSeconds <= 0 {
		c.CheckinSeconds = 60
	}
	return &c, nil
}

// Save writes the configuration, readable by its owner alone.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}
