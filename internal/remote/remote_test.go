//go:build !windows

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestTerminalSession stands in for the relay: it opens the session, types a
// command, and checks the shell's output comes back, all over a real
// WebSocket and a real PTY.
func TestTerminalSession(t *testing.T) {
	saw := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Open the session, then expect the agent's "ready".
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"open","cols":80,"rows":24}`))
		if typ, data, err := conn.Read(ctx); err != nil || typ != websocket.MessageText || !strings.Contains(string(data), "ready") {
			t.Errorf("expected a ready frame, got %q (%v)", data, err)
			return
		}

		// Type a command and read the shell's output until the marker shows up.
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("printf 'orbit-marker-%s\\n' done\n"))
		var out strings.Builder
		for ctx.Err() == nil {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				break
			}
			if typ == websocket.MessageBinary {
				out.Write(data)
				if strings.Contains(out.String(), "orbit-marker-done") {
					saw <- out.String()
					break
				}
			}
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	relay := "ws" + strings.TrimPrefix(srv.URL, "http")
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		done <- Run(ctx, Options{Mode: "terminal", Shell: "sh", Relay: relay, Secret: "test-secret", Version: "test"})
	}()

	select {
	case got := <-saw:
		if !strings.Contains(got, "orbit-marker-done") {
			t.Fatalf("the shell's output didn't come back: %q", got)
		}
	case <-time.After(18 * time.Second):
		t.Fatal("timed out waiting for the shell's output")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session didn't end after the relay closed")
	}
}

func TestItoa(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0"}, {5, "5"}, {5900, "5900"}, {65535, "65535"}} {
		if got := itoa(c.in); got != c.want {
			t.Errorf("itoa(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
