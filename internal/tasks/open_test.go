package tasks

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
)

func seal(t *testing.T, key ed25519.PrivateKey, task map[string]any) api.Sealed {
	t.Helper()
	envelope, _ := json.Marshal(task)
	return api.Sealed{
		ID:        task["id"].(string),
		Envelope:  base64.StdEncoding.EncodeToString(envelope),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, envelope)),
	}
}

func TestOpen(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	serverKey := base64.StdEncoding.EncodeToString(public)
	now := time.Now()
	good := map[string]any{"id": "t1", "agent": "a1", "kind": "script", "payload": map[string]any{"body": "echo"}, "issued": now.Unix(), "expires": now.Add(time.Hour).Unix()}
	seen := LoadSeen(t.TempDir())

	task, err := Open(seal(t, private, good), serverKey, "a1", now, seen)
	if err != nil || task.Kind != "script" {
		t.Fatalf("a good task was refused: %v", err)
	}

	sealed := seal(t, private, good)
	tampered := good
	tampered["kind"] = "uninstall"
	forged, _ := json.Marshal(tampered)
	sealed.Envelope = base64.StdEncoding.EncodeToString(forged)
	if _, err := Open(sealed, serverKey, "a1", now, seen); !errors.Is(err, ErrSignature) {
		t.Fatalf("a changed task was accepted: %v", err)
	}

	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Open(seal(t, stranger, map[string]any{"id": "t2", "agent": "a1", "kind": "script"}), serverKey, "a1", now, seen); !errors.Is(err, ErrSignature) {
		t.Fatalf("a task signed by another key was accepted: %v", err)
	}
	if _, err := Open(seal(t, private, map[string]any{"id": "t3", "agent": "a2", "kind": "script"}), serverKey, "a1", now, seen); !errors.Is(err, ErrNotForUs) {
		t.Fatalf("another agent's task was accepted: %v", err)
	}
	if _, err := Open(seal(t, private, map[string]any{"id": "t4", "agent": "a1", "kind": "script", "expires": now.Add(-time.Minute).Unix()}), serverKey, "a1", now, seen); !errors.Is(err, ErrExpired) {
		t.Fatalf("an expired task was accepted: %v", err)
	}
}

func TestSeenIsPersistedAndRefusesReplays(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	seen := LoadSeen(dir)
	if err := seen.Add("t1", time.Now()); err != nil {
		t.Fatal(err)
	}
	again := LoadSeen(dir)
	sealed := seal(t, private, map[string]any{"id": "t1", "agent": "a1", "kind": "script"})
	if _, err := Open(sealed, base64.StdEncoding.EncodeToString(public), "a1", time.Now(), again); !errors.Is(err, ErrReplayed) {
		t.Fatalf("a task that already ran was accepted again: %v", err)
	}
}
