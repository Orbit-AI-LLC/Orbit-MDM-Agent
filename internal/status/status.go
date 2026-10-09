// Package status is the small, world-readable file the service writes each
// check-in for the tray app in the signed-in person's session to read
// (internal/config.StatusPath). It carries no secrets: health, the last
// check-in, any problems the person can act on (on macOS, missing privacy
// permissions), and the organization's help-desk contact.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Severity ranks an issue for the tray.
const (
	Error   = "error"
	Warning = "warning"
	Info    = "info"
)

// Permission states, as the agent can tell them apart.
const (
	Granted = "granted"
	Denied  = "denied"
	Unknown = "unknown"
)

// Issue is one problem the tray shows, with an optional action the UI knows how
// to carry out (Fix, e.g. "open_screen_recording").
type Issue struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// Support is the organization's help desk, set in Orbit RMM and sent to the
// agent at check-in so the tray can show it.
type Support struct {
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Empty reports whether no help-desk contact is set.
func (s Support) Empty() bool { return s.Name == "" && s.Phone == "" && s.Email == "" && s.URL == "" }

// Status is the whole file.
type Status struct {
	Version       string            `json:"version"`
	Hostname      string            `json:"hostname,omitempty"`
	OS            string            `json:"os"`
	Enrolled      bool              `json:"enrolled"`
	Server        string            `json:"server,omitempty"`
	Device        string            `json:"device,omitempty"`
	LastCheckin   string            `json:"last_checkin,omitempty"`
	LastCheckinOK bool              `json:"last_checkin_ok"`
	Healthy       bool              `json:"healthy"`
	Issues        []Issue           `json:"issues"`
	Permissions   map[string]string `json:"permissions,omitempty"`
	Support       Support           `json:"support"`
	UpdatedAt     string            `json:"updated_at"`
}

// Write saves the status to path for anyone on the computer to read (0644),
// replacing it in one step so a reader never sees a half-written file.
func Write(path string, s Status) error {
	if s.Issues == nil {
		s.Issues = []Issue{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	// WriteFile honours the file's existing mode on some systems; make sure a
	// reader in the user's session can read it.
	_ = os.Chmod(tmp, 0o644)
	return os.Rename(tmp, path)
}

// Read loads a status file (the tray does this; kept here so both sides agree).
func Read(path string) (Status, error) {
	var s Status
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}
