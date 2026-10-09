package remote

import (
	"context"
	"encoding/json"

	"github.com/coder/websocket"
)

// terminal is a running shell behind a pseudo-terminal.
type terminal interface {
	Read(p []byte) (int, error)   // the shell's output
	Write(p []byte) (int, error)  // keystrokes to the shell
	Resize(cols, rows uint16) error
	Wait() int // block until the shell exits, and give its code
	Close() error
}

func runTerminal(ctx context.Context, l *link, opt Options, cols, rows uint16) error {
	term, err := openTerminal(opt.Shell, cols, rows)
	if err != nil {
		fail(l, "Couldn't start the shell: "+err.Error())
		return err
	}
	defer term.Close()
	_ = l.control(map[string]any{"type": "ready"})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go pipe(ctx, cancel, term, l)

	for {
		typ, data, err := l.conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			if _, err := term.Write(data); err != nil {
				break
			}
			continue
		}
		var frame struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &frame) == nil && frame.Type == "resize" && frame.Cols > 0 && frame.Rows > 0 {
			_ = term.Resize(frame.Cols, frame.Rows)
		}
	}

	// The shell exits when the browser closes the PTY's input, or at once if
	// the browser dropped; either way, tell the browser the exit code.
	term.Close()
	code := term.Wait()
	cancel()
	_ = l.control(map[string]any{"type": "exit", "code": code})
	return nil
}
