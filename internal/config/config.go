// Package config is where the agent keeps who it is and whom it trusts.
//
// The file holds the server's address, the agent's id and secret, and the
// server's Ed25519 public key, pinned at enrollment: every task must be signed
// with it. It is readable by root (or SYSTEM) alone.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Config is the agent's enrollment.
type Config struct {
	Server         string `json:"server"`
	AgentID        string `json:"agent_id"`
	Secret         string `json:"secret"`
	ServerKey      string `json:"server_key"`
	CheckinSeconds int    `json:"checkin_seconds"`
	Device         string `json:"device"`
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
