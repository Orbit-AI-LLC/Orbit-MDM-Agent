package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Seen remembers the tasks this agent has run, so none runs twice.
// Entries are kept until well after any task could expire.
type Seen struct {
	mu   sync.Mutex
	path string
	ids  map[string]int64
}

const keep = 30 * 24 * time.Hour

// LoadSeen reads the record from dir (made if missing).
func LoadSeen(dir string) *Seen {
	s := &Seen{path: filepath.Join(dir, "seen.json"), ids: map[string]int64{}}
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &s.ids)
	}
	return s
}

// Has reports whether a task has already run.
func (s *Seen) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

// Add records a task as run, before it runs, and saves the record.
func (s *Seen) Add(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids[id] = now.Unix()
	cutoff := now.Add(-keep).Unix()
	for k, at := range s.ids {
		if at < cutoff {
			delete(s.ids, k)
		}
	}
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.ids)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
