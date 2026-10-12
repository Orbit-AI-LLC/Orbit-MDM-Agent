// Command gen_vectors writes the golden wire vectors that pin the Orbit RMM
// agent's protocol, generated here (Go) and consumed by the Swift macOS agent's
// tests and, in time, the Python server's tests. The point is a single frozen
// corpus: a protocol change that forgets one implementation fails that side's CI.
//
// Run from the repository root:
//
//	go run ./scripts/gen_vectors
//
// It writes canonical copies to testdata/vectors and a copy into the Swift test
// bundle at macos-agent/Tests/OrbitAgentKitTests/Vectors.
//
// The task envelope is canonicalized exactly as the server's apps/rmm/signing.py
// does: compact JSON with sorted keys, Ed25519-signed over those bytes. Payloads
// avoid '<', '>' and '&' so Go's default HTML escaping can't diverge from
// Python's json.dumps.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// A fixed 32-byte Ed25519 seed so the vectors are deterministic across runs.
var serverSeed = []byte{
	0x9d, 0x61, 0xb1, 0x9d, 0xef, 0xfd, 0x5a, 0x60,
	0xba, 0x84, 0x4a, 0xf4, 0x92, 0xec, 0x2c, 0xc4,
	0x44, 0x49, 0xc5, 0x69, 0x7b, 0x32, 0x69, 0x19,
	0x70, 0x3b, 0xac, 0x03, 0x1c, 0xae, 0x7f, 0x60,
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// seal canonicalizes body the way the server does (compact, sorted keys) and
// signs it, returning the outer sealed envelope and the raw envelope bytes.
func seal(priv ed25519.PrivateKey, body map[string]any) (map[string]string, []byte) {
	// json.Marshal of a map emits compact JSON with keys sorted — the same bytes
	// Python's json.dumps(sort_keys=True, separators=(",",":")) produces.
	envelope, err := json.Marshal(body)
	if err != nil {
		log.Fatal(err)
	}
	sig := ed25519.Sign(priv, envelope)
	return map[string]string{
		"id":        body["id"].(string),
		"envelope":  b64(envelope),
		"signature": b64(sig),
	}, envelope
}

func main() {
	priv := ed25519.NewKeyFromSeed(serverSeed)
	pub := priv.Public().(ed25519.PublicKey)
	serverKey := b64(pub)
	const agentID = "11111111-2222-3333-4444-555555555555"

	// A good task.
	goodBody := map[string]any{
		"id":      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"agent":   agentID,
		"kind":    "script",
		"payload": map[string]any{"shell": "zsh", "body": "echo hello", "timeout": 30, "run_as": "root"},
		"issued":  1700000000,
		"expires": 1700000600,
	}
	good, _ := seal(priv, goodBody)

	// A task for a different agent (must be rejected: notForUs).
	otherBody := map[string]any{
		"id": "ffffffff-0000-1111-2222-333333333333", "agent": "someone-else",
		"kind": "reboot", "payload": nil, "issued": 1700000000, "expires": 0,
	}
	other, _ := seal(priv, otherBody)

	// An expired task (expires in the past).
	expiredBody := map[string]any{
		"id": "99999999-8888-7777-6666-555555555555", "agent": agentID,
		"kind": "lock", "payload": nil, "issued": 1600000000, "expires": 1600000600,
	}
	expired, _ := seal(priv, expiredBody)

	// A tampered task: a good envelope with one signature byte flipped (must be
	// rejected: signature).
	tamperedSig, _ := base64.StdEncoding.DecodeString(good["signature"])
	tamperedSig[0] ^= 0x01
	tampered := map[string]string{
		"id": good["id"], "envelope": good["envelope"], "signature": b64(tamperedSig),
	}

	vector := map[string]any{
		"note":       "Ed25519 verify-then-parse vectors for internal/tasks/open.go and the Swift Tasks.open. Verify the received envelope bytes, then parse THOSE bytes.",
		"server_key": serverKey,
		"agent_id":   agentID,
		"now_unix":   1700000300, // between issued and expires for the good task
		"good": map[string]any{
			"sealed": good,
			"expect": map[string]any{
				"id": goodBody["id"], "agent": agentID, "kind": "script",
				"issued": 1700000000, "expires": 1700000600,
			},
		},
		"not_for_us": map[string]any{"sealed": other, "expect_error": "notForUs"},
		"expired":    map[string]any{"sealed": expired, "expect_error": "expired"},
		"tampered":   map[string]any{"sealed": tampered, "expect_error": "signature"},
	}

	out, err := json.MarshalIndent(vector, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range []string{
		"testdata/vectors",
		"macos-agent/Tests/OrbitAgentKitTests/Vectors",
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(dir, "task_verify.json")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", path)
	}
}
