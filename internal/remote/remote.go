// Package remote joins a live session the server opened: a terminal (a real
// PTY running the computer's shell) or a remote desktop (the computer's VNC
// server), piped to the admin's browser through the relay in Orbit MDM
// (apps/rmm/remote.py).
//
// A signed "remote" task brings the agent here with where to connect and which
// session to join. The agent opens a WebSocket to the relay, authenticated
// with its own secret, waits for the relay's "open" frame, then passes bytes:
// binary frames are the terminal's bytes or the desktop's RFB stream; text
// frames are JSON control messages ("resize" in, "ready"/"exit"/"error" out).
//
// This file's frames and apps/rmm/remote.py must agree; change both together.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Options is a remote session to join, from the task's payload.
type Options struct {
	Session string // the session's id, for logging
	Mode    string // "terminal" or "desktop"
	Shell   string // the terminal's shell; empty for a desktop
	Relay   string // the relay's ws(s):// URL
	By      string // who opened it, for the on-screen notice
	Secret  string // this agent's secret, to authenticate to the relay
	Version string
	Logger  *log.Logger
}

const (
	// How much of each side's stream to carry at once.
	bufferSize = 32 * 1024
	// The largest frame the browser may send us (pastes, RFB client messages).
	readLimit = 1 << 20
	writeWait = 30 * time.Second
)

// link is the relay socket, with writes serialized (one writer at a time).
type link struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (l *link) write(typ websocket.MessageType, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeWait)
	defer cancel()
	return l.conn.Write(ctx, typ, data)
}

func (l *link) binary(data []byte) error { return l.write(websocket.MessageBinary, data) }

func (l *link) control(frame map[string]any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return l.write(websocket.MessageText, data)
}

// Run joins a session and returns when it ends.
func Run(ctx context.Context, opt Options) error {
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+opt.Secret)
	header.Set("User-Agent", "orbit-agent/"+opt.Version)

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, _, err := websocket.Dial(dialCtx, opt.Relay, &websocket.DialOptions{HTTPHeader: header})
	cancel()
	if err != nil {
		return fmt.Errorf("couldn't reach the relay: %w", err)
	}
	conn.SetReadLimit(readLimit)
	defer conn.CloseNow()
	l := &link{conn: conn}

	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	typ, data, err := conn.Read(openCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("the relay didn't open the session: %w", err)
	}
	var open struct {
		Type string `json:"type"`
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	if typ != websocket.MessageText || json.Unmarshal(data, &open) != nil || open.Type != "open" {
		return errors.New("the relay sent something unexpected")
	}

	switch opt.Mode {
	case "terminal":
		err = runTerminal(ctx, l, opt, open.Cols, open.Rows)
	case "desktop":
		err = runDesktop(ctx, l, opt)
	default:
		err = fmt.Errorf("unknown session mode %q", opt.Mode)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return err
}

// pipe copies a local stream (the PTY or the VNC connection) to the relay as
// binary frames, cancelling the session when either end stops.
func pipe(ctx context.Context, cancel context.CancelFunc, src io.Reader, l *link) {
	defer cancel()
	buf := make([]byte, bufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if werr := l.binary(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func fail(l *link, message string) {
	_ = l.control(map[string]any{"type": "error", "message": message})
}
