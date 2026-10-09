package pulse

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// serverSide plays apps/rmm/pulse.py: it authenticates with serverKey, checks
// the agent against agentPub, and returns a session oriented for the server. It
// returns errors (never touches *testing.T) so it is safe in a goroutine.
func serverSide(conn net.Conn, serverKey ed25519.PrivateKey, agentPub ed25519.PublicKey) (*session, error) {
	helloBytes, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	var hello struct{ Agent, CN, CE string }
	if err := json.Unmarshal(helloBytes, &hello); err != nil {
		return nil, err
	}
	clientNonce, _ := unb64(hello.CN, 32)
	clientEph, _ := unb64(hello.CE, 32)

	serverNonce := make([]byte, 32)
	rand.Read(serverNonce)
	curve := ecdh.X25519()
	ephPriv, _ := curve.GenerateKey(rand.Reader)
	serverEph := ephPriv.PublicKey().Bytes()
	transcript := append([]byte(protocol+"\x00"), concat(clientNonce, clientEph, serverNonce, serverEph)...)
	reply, _ := json.Marshal(map[string]any{
		"sn": b64(serverNonce), "se": b64(serverEph), "sig": b64(ed25519.Sign(serverKey, transcript)),
	})
	if err := writeFrame(conn, reply); err != nil {
		return nil, err
	}

	authBytes, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	var auth struct{ Sig string }
	json.Unmarshal(authBytes, &auth)
	sig, _ := unb64(auth.Sig, 64)
	if !ed25519.Verify(agentPub, transcript, sig) {
		return nil, errors.New("agent signature didn't verify")
	}

	remote, _ := curve.NewPublicKey(clientEph)
	shared, _ := ephPriv.ECDH(remote)
	// Opposite orientation to the client's newSession: the server sends s2a.
	s2a, _ := derive(shared, concat(clientNonce, serverNonce), "orbit-pulse/1 server->agent")
	a2s, _ := derive(shared, concat(clientNonce, serverNonce), "orbit-pulse/1 agent->server")
	send, _ := chacha20poly1305.New(s2a)
	recv, _ := chacha20poly1305.New(a2s)
	return &session{send0: send, recv0: recv}, nil
}

func TestHandshakeAndNudge(t *testing.T) {
	_, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	agentPub, agentPriv, _ := ed25519.GenerateKey(rand.Reader)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	type result struct {
		sess *session
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sess, err := serverSide(server, serverKey, agentPub)
		if err == nil {
			err = sess.send(server, frame{T: "ready"})
		}
		ch <- result{sess, err}
	}()

	sess, err := handshake(client, Options{AgentID: "a-1", ServerKey: serverKey.Public().(ed25519.PublicKey), Signer: agentPriv})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("server side: %v", r.err)
	}

	go r.sess.send(server, frame{T: "nudge"})
	got, err := sess.recv(client)
	if err != nil || got.T != "nudge" {
		t.Fatalf("expected a nudge, got %q (%v)", got.T, err)
	}

	go sess.send(client, frame{T: "pong"})
	back, err := r.sess.recv(server)
	if err != nil || back.T != "pong" {
		t.Fatalf("server read %q (%v)", back.T, err)
	}
}

func TestHandshakeRejectsAnUntrustedServer(t *testing.T) {
	_, realServerKey, _ := ed25519.GenerateKey(rand.Reader)
	impostorPub, _, _ := ed25519.GenerateKey(rand.Reader) // the key the agent trusts
	agentPub, agentPriv, _ := ed25519.GenerateKey(rand.Reader)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go serverSide(server, realServerKey, agentPub) // error ignored; the client should bail

	if _, err := handshake(client, Options{AgentID: "a-1", ServerKey: impostorPub, Signer: agentPriv}); err == nil {
		t.Fatal("handshake accepted a server signing with the wrong key")
	}
}

func TestMaintainStopsWithContext(t *testing.T) {
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { Maintain(ctx, Options{Addr: "127.0.0.1:1"}); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Maintain didn't stop when the context was cancelled")
	}
}
