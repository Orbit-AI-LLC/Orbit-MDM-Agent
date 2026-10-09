// Package tasks checks that a task really came from the server before
// anything runs it.
//
// The server signs the exact bytes of each task with its Ed25519 key, the key
// this agent pinned when it enrolled. A task is run only if the signature is
// good, it names this agent, it hasn't expired, and it hasn't been run before:
// so neither a man in the middle nor someone who copied the server's database
// can make this computer run anything.
package tasks

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Orbit-AI-LLC/Orbit-MDM-Agent/internal/api"
)

// Task is a verified task.
type Task struct {
	ID      string          `json:"id"`
	Agent   string          `json:"agent"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	Issued  int64           `json:"issued"`
	Expires int64           `json:"expires"`
}

// Errors a task can be refused with.
var (
	ErrSignature = errors.New("the task's signature is not the server's")
	ErrNotForUs  = errors.New("the task is for another agent")
	ErrExpired   = errors.New("the task has expired")
	ErrReplayed  = errors.New("the task has already run")
)

// Open verifies a sealed task and returns it.
func Open(sealed api.Sealed, serverKey, agentID string, now time.Time, seen *Seen) (*Task, error) {
	key, err := base64.StdEncoding.DecodeString(serverKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the pinned server key is unusable: %w", ErrSignature)
	}
	envelope, err := base64.StdEncoding.DecodeString(sealed.Envelope)
	if err != nil {
		return nil, ErrSignature
	}
	signature, err := base64.StdEncoding.DecodeString(sealed.Signature)
	if err != nil {
		return nil, ErrSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(key), envelope, signature) {
		return nil, ErrSignature
	}
	var task Task
	if err := json.Unmarshal(envelope, &task); err != nil {
		return nil, ErrSignature
	}
	if task.Agent != agentID {
		return nil, ErrNotForUs
	}
	if task.Expires != 0 && now.Unix() > task.Expires {
		return nil, ErrExpired
	}
	if seen != nil && seen.Has(task.ID) {
		return nil, ErrReplayed
	}
	return &task, nil
}
