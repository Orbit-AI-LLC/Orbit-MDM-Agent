package tasks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
)

// TestGoldenVectors checks the shared golden vectors (scripts/gen_vectors, also
// consumed by the Swift macOS agent's tests) against this package's real Open, so
// that "the Swift agent matches the vectors" is the same as "the Swift agent
// matches the Go agent". Regenerate with: go run ./scripts/gen_vectors
func TestGoldenVectors(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "vectors", "task_verify.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors (run `go run ./scripts/gen_vectors`): %v", err)
	}
	var v struct {
		ServerKey string `json:"server_key"`
		AgentID   string `json:"agent_id"`
		NowUnix   int64  `json:"now_unix"`
		Good      struct {
			Sealed api.Sealed `json:"sealed"`
			Expect struct {
				ID, Agent, Kind string
				Issued, Expires int64
			} `json:"expect"`
		} `json:"good"`
		NotForUs struct {
			Sealed api.Sealed `json:"sealed"`
		} `json:"not_for_us"`
		Expired struct {
			Sealed api.Sealed `json:"sealed"`
		} `json:"expired"`
		Tampered struct {
			Sealed api.Sealed `json:"sealed"`
		} `json:"tampered"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	now := time.Unix(v.NowUnix, 0)

	task, err := Open(v.Good.Sealed, v.ServerKey, v.AgentID, now, nil)
	if err != nil {
		t.Fatalf("good task rejected: %v", err)
	}
	if task.ID != v.Good.Expect.ID || task.Agent != v.Good.Expect.Agent ||
		task.Kind != v.Good.Expect.Kind || task.Issued != v.Good.Expect.Issued ||
		task.Expires != v.Good.Expect.Expires {
		t.Fatalf("good task fields mismatch: %+v", task)
	}

	if _, err := Open(v.NotForUs.Sealed, v.ServerKey, v.AgentID, now, nil); !errors.Is(err, ErrNotForUs) {
		t.Fatalf("not_for_us: got %v, want ErrNotForUs", err)
	}
	if _, err := Open(v.Expired.Sealed, v.ServerKey, v.AgentID, now, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: got %v, want ErrExpired", err)
	}
	if _, err := Open(v.Tampered.Sealed, v.ServerKey, v.AgentID, now, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered: got %v, want ErrSignature", err)
	}
}
