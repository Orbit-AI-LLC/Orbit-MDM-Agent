// Package pulse keeps one long-lived connection open from this computer to the
// server so work reaches the agent the moment it is queued, instead of waiting
// for the next check-in.
//
// The agent always dials out; the server only accepts, so nothing listens on
// the computer. The link carries only keepalives and a "nudge" ("check in
// now") — never a task. Tasks still arrive signed over HTTPS, so even someone
// who owned the link could do no more than make the agent check in early.
//
// The link is raw TCP (no TLS through the server's proxy), so it is
// authenticated and encrypted here. The server proves itself by signing the
// handshake transcript with the Ed25519 key this agent pinned at enrollment; a
// fake or intercepting server can't, and is dropped. The agent proves itself by
// signing the same transcript with its own key, registered over the
// authenticated API. Both sign a fresh X25519 exchange into the transcript, so
// a man in the middle can't substitute keys and the session is forward secret;
// every frame after the handshake is ChaCha20-Poly1305. This mirrors the
// server's apps/rmm/pulse.py; the two must agree.
package pulse

import (
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mrand "math/rand/v2"
	"net"
	"sync"
	"time"

	"crypto/sha256"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	protocol         = "orbit-pulse/1"
	maxFrame         = 4096
	dialTimeout      = 20 * time.Second
	handshakeTimeout = 15 * time.Second
	// Ping while idle, and give up on a link we've heard nothing on for
	// idleTimeout — the server does the same, so a dropped agent goes offline
	// in the portal within seconds. These mirror apps/rmm/pulse.py.
	pingEvery   = 15 * time.Second
	idleTimeout = 40 * time.Second
	// A TCP keepalive so a link cut without a close (a sleep, a dead router) is
	// noticed by the kernel too, not only by the ping.
	tcpKeepAlive = 15 * time.Second
)

// Options is what a pulse connection needs.
type Options struct {
	Addr      string             // the server's host:port
	AgentID   string             // who this agent is
	ServerKey ed25519.PublicKey  // the pinned server key, to check the server is genuine
	Signer    ed25519.PrivateKey // this agent's key, to prove who it is
	OnNudge   func()             // run when the server says to check in now
	Logger    *log.Logger
}

// Maintain keeps the connection up until ctx ends, reconnecting with backoff
// whenever it drops. It returns only when ctx is done.
func Maintain(ctx context.Context, opt Options) {
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		if err := connect(ctx, opt); err != nil && ctx.Err() == nil {
			opt.Logger.Printf("pulse: %v", err)
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second // the link lasted; next drop retries promptly
		}
		jitter := time.Duration(mrand.Int64N(int64(backoff)/2 + 1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff + jitter):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func connect(ctx context.Context, opt Options) error {
	dialer := net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
	conn, err := dialer.DialContext(ctx, "tcp", opt.Addr)
	if err != nil {
		return fmt.Errorf("couldn't reach the pulse server: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	sess, err := handshake(conn, opt)
	if err != nil {
		return fmt.Errorf("pulse handshake: %w", err)
	}
	opt.Logger.Printf("pulse: connected to %s", opt.Addr)
	// Check in right after connecting, so work queued while the link was down
	// (or while reconnecting) is picked up without waiting for the next nudge.
	if opt.OnNudge != nil {
		opt.OnNudge()
	}
	return serve(ctx, conn, sess, opt)
}

// serve reads frames until the link drops, pinging while idle so a dead link is
// noticed. A "nudge" triggers a check-in.
func serve(ctx context.Context, conn net.Conn, sess *session, opt Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(pingEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sess.send(conn, frame{T: "ping"}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		msg, err := sess.recv(conn)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch msg.T {
		case "nudge":
			if opt.OnNudge != nil {
				opt.OnNudge()
			}
		case "ping":
			if err := sess.send(conn, frame{T: "pong"}); err != nil {
				return err
			}
		}
	}
}

// --- the handshake ------------------------------------------------------------

func handshake(conn net.Conn, opt Options) (*session, error) {
	curve := ecdh.X25519()
	ephPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	clientNonce := make([]byte, 32)
	if _, err := rand.Read(clientNonce); err != nil {
		return nil, err
	}
	clientEph := ephPriv.PublicKey().Bytes()

	hello, _ := json.Marshal(map[string]any{
		"v": 1, "agent": opt.AgentID,
		"cn": b64(clientNonce), "ce": b64(clientEph),
	})
	if err := writeFrame(conn, hello); err != nil {
		return nil, err
	}

	replyBytes, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	var reply struct {
		SN  string `json:"sn"`
		SE  string `json:"se"`
		Sig string `json:"sig"`
	}
	if err := json.Unmarshal(replyBytes, &reply); err != nil {
		return nil, err
	}
	serverNonce, err1 := unb64(reply.SN, 32)
	serverEph, err2 := unb64(reply.SE, 32)
	serverSig, err3 := base64.StdEncoding.DecodeString(reply.Sig)
	if err := firstErr(err1, err2, err3); err != nil {
		return nil, err
	}

	transcript := append([]byte(protocol+"\x00"), concat(clientNonce, clientEph, serverNonce, serverEph)...)
	if !ed25519.Verify(opt.ServerKey, transcript, serverSig) {
		return nil, errors.New("the server's signature didn't verify; not a trusted server")
	}

	auth, _ := json.Marshal(map[string]any{"sig": b64(ed25519.Sign(opt.Signer, transcript))})
	if err := writeFrame(conn, auth); err != nil {
		return nil, err
	}

	remoteEph, err := curve.NewPublicKey(serverEph)
	if err != nil {
		return nil, err
	}
	shared, err := ephPriv.ECDH(remoteEph)
	if err != nil {
		return nil, err
	}
	sess, err := newSession(shared, concat(clientNonce, serverNonce))
	if err != nil {
		return nil, err
	}
	// The server's first sealed frame confirms it derived the same keys.
	if _, err := sess.recv(conn); err != nil {
		return nil, fmt.Errorf("the server didn't confirm the session: %w", err)
	}
	return sess, nil
}

// --- the sealed session -------------------------------------------------------

type frame struct {
	T string `json:"t"`
}

type session struct {
	send0   cipher.AEAD
	recv0   cipher.AEAD
	sendMu  sync.Mutex // one writer at a time: the pinger and the pong reply both send
	sendCtr uint64
	recvCtr uint64
}

func newSession(shared, salt []byte) (*session, error) {
	a2s, err1 := derive(shared, salt, "orbit-pulse/1 agent->server")
	s2a, err2 := derive(shared, salt, "orbit-pulse/1 server->agent")
	if err := firstErr(err1, err2); err != nil {
		return nil, err
	}
	sendAEAD, err1 := chacha20poly1305.New(a2s)
	recvAEAD, err2 := chacha20poly1305.New(s2a)
	if err := firstErr(err1, err2); err != nil {
		return nil, err
	}
	return &session{send0: sendAEAD, recv0: recvAEAD}, nil
}

func nonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

func (s *session) send(conn net.Conn, f frame) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	plaintext, _ := json.Marshal(f)
	box := s.send0.Seal(nil, nonce(s.sendCtr), plaintext, nil)
	s.sendCtr++
	_ = conn.SetWriteDeadline(time.Now().Add(pingEvery))
	return writeFrame(conn, box)
}

func (s *session) recv(conn net.Conn) (frame, error) {
	box, err := readFrame(conn)
	if err != nil {
		return frame{}, err
	}
	plaintext, err := s.recv0.Open(nil, nonce(s.recvCtr), box, nil)
	if err != nil {
		return frame{}, errors.New("a pulse frame didn't authenticate")
	}
	s.recvCtr++
	var f frame
	_ = json.Unmarshal(plaintext, &f)
	return f, nil
}

func derive(ikm, salt []byte, info string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte(info)), key); err != nil {
		return nil, err
	}
	return key, nil
}

// --- length-prefixed frames ---------------------------------------------------

func writeFrame(conn net.Conn, data []byte) error {
	if len(data) > maxFrame {
		return errors.New("pulse frame too large")
	}
	header := []byte{byte(len(data) >> 8), byte(len(data))}
	if _, err := conn.Write(append(header, data...)); err != nil {
		return err
	}
	return nil
}

func readFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := int(header[0])<<8 | int(header[1])
	if length == 0 || length > maxFrame {
		return nil, errors.New("pulse frame out of range")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

// --- small helpers ------------------------------------------------------------

func b64(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func unb64(value string, length int) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(raw) != length {
		return nil, fmt.Errorf("expected %d bytes, got %d", length, len(raw))
	}
	return raw, nil
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
