// A small VNC (RFB) server the agent runs on the computer itself, bound to
// loopback, so a remote desktop works without anyone turning on the operating
// system's screen sharing. It speaks just enough of RFB 3.8 for noVNC: no
// authentication (the only client is this agent, reached over the authenticated
// relay; nothing on the network can connect), one full-screen rectangle on
// request, Zlib-compressed when the client offers it (a desktop is mostly flat
// colour, so raw frames would be far too large) and raw otherwise.
//
// dialDesktop starts this and connects to it over an in-process pipe when no
// external VNC server is answering, so the relay and noVNC are unchanged.
package remote

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"net"
	"time"
)

// Screen is the computer's display and input, implemented per platform.
type Screen interface {
	// Size is the display in pixels (fixed for the session).
	Size() (width, height int)
	// Frame is the current screen as 32-bit pixels, 4 bytes each — blue, green,
	// red, unused — width*height*4 bytes, matching the format ServerInit sends.
	Frame() (width, height int, pixels []byte, err error)
	// Pointer moves to x,y with the given buttons (bit 0 left, 1 middle, 2 right).
	Pointer(x, y int, buttons uint8)
	// Key presses or releases a key by its X11 keysym.
	Key(sym uint32, down bool)
	Close()
}

// ErrNoBuiltInScreen means this platform has no built-in capture; the computer's
// own VNC server is used instead.
var ErrNoBuiltInScreen = errors.New("no built-in screen capture on this platform")

const (
	vncMinInterval = 250 * time.Millisecond // at most ~4 frames a second
	vncName        = "Orbit RMM"
	encRaw         = 0
	encZlib        = 6
)

type vncServer struct {
	conn    net.Conn
	screen  Screen
	useZlib bool
	zlib    *zlib.Writer
	zbuf    bytes.Buffer
}

// serveVNC speaks RFB to conn (one end of the pipe to the relay) until it closes.
func serveVNC(ctx context.Context, conn net.Conn, screen Screen) error {
	defer screen.Close()
	s := &vncServer{conn: conn, screen: screen}
	if err := s.handshake(); err != nil {
		return err
	}
	return s.loop(ctx)
}

func (s *vncServer) handshake() error {
	// Protocol version: offer 3.8, read the client's answer.
	if _, err := s.conn.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	if _, err := io.ReadFull(s.conn, make([]byte, 12)); err != nil {
		return err
	}
	// Security: the one type is None (1). The client picks it; then a result.
	if _, err := s.conn.Write([]byte{1, 1}); err != nil {
		return err
	}
	if _, err := io.ReadFull(s.conn, make([]byte, 1)); err != nil {
		return err
	}
	if _, err := s.conn.Write([]byte{0, 0, 0, 0}); err != nil { // SecurityResult: OK
		return err
	}
	if _, err := io.ReadFull(s.conn, make([]byte, 1)); err != nil { // ClientInit, shared-flag
		return err
	}
	return s.serverInit()
}

func (s *vncServer) serverInit() error {
	width, height := s.screen.Size()
	buf := make([]byte, 0, 24+len(vncName))
	buf = binary.BigEndian.AppendUint16(buf, uint16(width))
	buf = binary.BigEndian.AppendUint16(buf, uint16(height))
	// Pixel format: 32 bpp, depth 24, little-endian, true colour, max 255 each,
	// red shift 16, green 8, blue 0 — bytes blue, green, red, unused. This is
	// noVNC's own default, so it needs no conversion.
	buf = append(buf, 32, 24, 0, 1)
	buf = binary.BigEndian.AppendUint16(buf, 255)
	buf = binary.BigEndian.AppendUint16(buf, 255)
	buf = binary.BigEndian.AppendUint16(buf, 255)
	buf = append(buf, 16, 8, 0, 0, 0, 0)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(vncName)))
	buf = append(buf, vncName...)
	_, err := s.conn.Write(buf)
	return err
}

func (s *vncServer) loop(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = s.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	}()
	var lastFrame time.Time
	header := make([]byte, 1)
	for {
		if _, err := io.ReadFull(s.conn, header); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch header[0] {
		case 0: // SetPixelFormat — read and ignore (we send noVNC's default)
			if _, err := io.ReadFull(s.conn, make([]byte, 19)); err != nil {
				return err
			}
		case 2: // SetEncodings
			if err := s.readEncodings(); err != nil {
				return err
			}
		case 3: // FramebufferUpdateRequest
			if _, err := io.ReadFull(s.conn, make([]byte, 9)); err != nil {
				return err
			}
			if wait := vncMinInterval - time.Since(lastFrame); wait > 0 {
				time.Sleep(wait)
			}
			lastFrame = time.Now()
			if err := s.sendFrame(); err != nil {
				return err
			}
		case 4: // KeyEvent: down-flag(1), pad(2), key(4)
			body := make([]byte, 7)
			if _, err := io.ReadFull(s.conn, body); err != nil {
				return err
			}
			s.screen.Key(binary.BigEndian.Uint32(body[3:]), body[0] != 0)
		case 5: // PointerEvent: button-mask(1), x(2), y(2)
			body := make([]byte, 5)
			if _, err := io.ReadFull(s.conn, body); err != nil {
				return err
			}
			s.screen.Pointer(int(binary.BigEndian.Uint16(body[1:])), int(binary.BigEndian.Uint16(body[3:])), body[0])
		case 6: // ClientCutText: pad(3), length(4), text
			pad := make([]byte, 7)
			if _, err := io.ReadFull(s.conn, pad); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, s.conn, int64(binary.BigEndian.Uint32(pad[3:]))); err != nil {
				return err
			}
		default:
			return errors.New("unexpected RFB message")
		}
	}
}

func (s *vncServer) readEncodings() error {
	head := make([]byte, 3) // pad(1), number-of-encodings(2)
	if _, err := io.ReadFull(s.conn, head); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(head[1:]))
	list := make([]byte, 4*n)
	if _, err := io.ReadFull(s.conn, list); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if int32(binary.BigEndian.Uint32(list[4*i:])) == encZlib {
			s.useZlib = true
		}
	}
	return nil
}

// sendFrame sends the whole screen as one rectangle, Zlib if the client offered
// it, else raw.
func (s *vncServer) sendFrame() error {
	width, height, pixels, err := s.screen.Frame()
	if err != nil {
		return err
	}
	encoding := encRaw
	body := pixels
	if s.useZlib {
		encoding = encZlib
		body, err = s.deflate(pixels)
		if err != nil {
			return err
		}
	}
	head := make([]byte, 0, 20)
	head = append(head, 0, 0) // FramebufferUpdate, padding
	head = binary.BigEndian.AppendUint16(head, 1)
	head = binary.BigEndian.AppendUint16(head, 0)
	head = binary.BigEndian.AppendUint16(head, 0)
	head = binary.BigEndian.AppendUint16(head, uint16(width))
	head = binary.BigEndian.AppendUint16(head, uint16(height))
	head = binary.BigEndian.AppendUint32(head, uint32(encoding))
	if s.useZlib { // Zlib rectangles carry a 4-byte length before the data
		head = binary.BigEndian.AppendUint32(head, uint32(len(body)))
	}
	if _, err := s.conn.Write(head); err != nil {
		return err
	}
	_, err = s.conn.Write(body)
	return err
}

// fitWithin scales w,h down so neither exceeds max, keeping the aspect ratio.
func fitWithin(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, h * max / w
	}
	return w * max / h, max
}

// toBGRX nearest-neighbour scales any image to dstW×dstH and writes it as
// blue, green, red, unused — the pixel format ServerInit declares.
func toBGRX(img image.Image, dstW, dstH int) []byte {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	out := make([]byte, dstW*dstH*4)
	i := 0
	for y := 0; y < dstH; y++ {
		sy := b.Min.Y + y*srcH/dstH
		for x := 0; x < dstW; x++ {
			sx := b.Min.X + x*srcW/dstW
			r, g, bl, _ := img.At(sx, sy).RGBA() // 16-bit per channel
			out[i] = byte(bl >> 8)
			out[i+1] = byte(g >> 8)
			out[i+2] = byte(r >> 8)
			out[i+3] = 0
			i += 4
		}
	}
	return out
}

// deflate runs the pixels through one zlib stream kept for the whole session,
// flushing after each frame — what noVNC's Zlib decoder expects.
func (s *vncServer) deflate(pixels []byte) ([]byte, error) {
	// One writer for the whole session (never reset — that would start a new
	// stream noVNC wouldn't expect); only the buffer is cleared each frame.
	if s.zlib == nil {
		s.zlib = zlib.NewWriter(&s.zbuf)
	}
	s.zbuf.Reset()
	if _, err := s.zlib.Write(pixels); err != nil {
		return nil, err
	}
	if err := s.zlib.Flush(); err != nil {
		return nil, err
	}
	return s.zbuf.Bytes(), nil
}
