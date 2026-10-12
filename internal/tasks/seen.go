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

// Add reserves a task as run, before it runs, and saves the record. It reports
// whether the id was newly recorded: a false means the task was already seen (or
// already claimed by another copy still in flight), so the caller must not run
// it. The check and the record happen under one lock, so two copies of the same
// task that both passed Has can never both be admitted.
func (s *Seen) Add(id string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false, nil
	}
	s.ids[id] = now.Unix()
	cutoff := now.Add(-keep).Unix()
	for k, at := range s.ids {
		if at < cutoff {
			delete(s.ids, k)
		}
	}
	if s.path == "" {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return true, err
	}
	data, err := json.Marshal(s.ids)
	if err != nil {
		return true, err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return true, err
	}
	return true, os.Rename(tmp, s.path)
}
