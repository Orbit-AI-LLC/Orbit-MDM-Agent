package remote

import (
	"context"
	"net"
	"time"

	"github.com/coder/websocket"
)

// runDesktop connects to the computer's VNC server and passes its RFB stream
// to the browser's noVNC, which handles the protocol and any sign-in itself.
func runDesktop(ctx context.Context, l *link, opt Options) error {
	conn, cleanup, err := dialDesktop(ctx, opt)
	if err != nil {
		fail(l, err.Error())
		return err
	}
	defer cleanup()
	defer conn.Close()
	notifyViewing(opt.By)
	_ = l.control(map[string]any{"type": "ready"})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go pipe(ctx, cancel, conn, l)

	for {
		typ, data, err := l.conn.Read(ctx)
		if err != nil {
			return nil
		}
		if typ == websocket.MessageBinary {
			if _, err := conn.Write(data); err != nil {
				return nil
			}
		}
	}
}

// dialLocal connects to the first VNC server answering on localhost.
func dialLocal(ctx context.Context, ports []int) net.Conn {
	var dialer net.Dialer
	for _, port := range ports {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := dialer.DialContext(attempt, "tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		cancel()
		if err == nil {
			return conn
		}
	}
	return nil
}

func itoa(port int) string {
	if port == 0 {
		return "0"
	}
	var b [5]byte
	i := len(b)
	for port > 0 {
		i--
		b[i] = byte('0' + port%10)
		port /= 10
	}
	return string(b[i:])
}
