package remote

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeScreen struct {
	mu      sync.Mutex
	w, h    int
	pointer [3]int // x, y, buttons
	keySym  uint32
	keyDown bool
	closed  bool
}

func (s *fakeScreen) Size() (int, int) { return s.w, s.h }

func (s *fakeScreen) Frame() (int, int, []byte, error) {
	return s.w, s.h, make([]byte, s.w*s.h*4), nil
}

func (s *fakeScreen) Pointer(x, y int, buttons uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pointer = [3]int{x, y, int(buttons)}
}

func (s *fakeScreen) Key(sym uint32, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keySym, s.keyDown = sym, down
}

func (s *fakeScreen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// vncClientHandshake runs the client half up to the point where frames can be
// requested, and returns the framebuffer size from ServerInit.
func vncClientHandshake(t *testing.T, client net.Conn) (int, int) {
	t.Helper()
	read := func(n int) []byte {
		b := make([]byte, n)
		if _, err := io.ReadFull(client, b); err != nil {
			t.Fatalf("read %d: %v", n, err)
		}
		return b
	}
	if got := string(read(12)); got != "RFB 003.008\n" {
		t.Fatalf("version = %q", got)
	}
	client.Write([]byte("RFB 003.008\n"))
	read(2)                 // security types
	client.Write([]byte{1}) // choose None
	read(4)                 // SecurityResult
	client.Write([]byte{1}) // ClientInit
	init := read(24 + len(vncName))
	return int(binary.BigEndian.Uint16(init[0:])), int(binary.BigEndian.Uint16(init[2:]))
}

func TestFitWithin(t *testing.T) {
	cases := []struct{ w, h, max, wantW, wantH int }{
		{800, 600, 1440, 800, 600},    // already small enough
		{2880, 1800, 1440, 1440, 900}, // landscape retina
		{1200, 2400, 1440, 720, 1440}, // portrait
	}
	for _, c := range cases {
		if w, h := fitWithin(c.w, c.h, c.max); w != c.wantW || h != c.wantH {
			t.Errorf("fitWithin(%d,%d,%d) = %d,%d want %d,%d", c.w, c.h, c.max, w, h, c.wantW, c.wantH)
		}
	}
}

func TestToBGRX(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	src.Set(1, 0, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	out := toBGRX(src, 2, 1)
	// Blue, green, red, unused for each pixel.
	want := []byte{30, 20, 10, 0, 60, 50, 40, 0}
	if !bytes.Equal(out, want) {
		t.Fatalf("toBGRX = %v want %v", out, want)
	}
}

func TestVNCZlibFrame(t *testing.T) {
	screen := &fakeScreen{w: 8, h: 8}
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveVNC(ctx, server, screen) }()

	w, h := vncClientHandshake(t, client)

	// Offer Zlib (6) via SetEncodings, then request a frame.
	enc := []byte{2, 0}
	enc = binary.BigEndian.AppendUint16(enc, 1)
	enc = binary.BigEndian.AppendUint32(enc, 6)
	client.Write(enc)
	req := []byte{3, 0, 0, 0, 0, 0}
	req = binary.BigEndian.AppendUint16(req, uint16(w))
	req = binary.BigEndian.AppendUint16(req, uint16(h))
	client.Write(req)

	head := make([]byte, 20) // rect header (16) + zlib length (4)
	if _, err := io.ReadFull(client, head); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if enc := binary.BigEndian.Uint32(head[12:16]); enc != 6 {
		t.Fatalf("encoding = %d (want zlib)", enc)
	}
	n := binary.BigEndian.Uint32(head[16:20])
	comp := make([]byte, n)
	if _, err := io.ReadFull(client, comp); err != nil {
		t.Fatalf("read zlib body: %v", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(comp))
	if err != nil {
		t.Fatalf("zlib: %v", err)
	}
	// A sync-flushed chunk isn't a terminated stream (noVNC streams it); read
	// exactly the frame's bytes rather than to EOF.
	out := make([]byte, w*h*4)
	if _, err := io.ReadFull(zr, out); err != nil {
		t.Fatalf("inflate: %v", err)
	}
	cancel()
	client.Close()
	<-done
}

func TestVNCHandshakeFrameAndInput(t *testing.T) {
	screen := &fakeScreen{w: 4, h: 2}
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveVNC(ctx, server, screen) }()

	read := func(n int) []byte {
		b := make([]byte, n)
		if _, err := io.ReadFull(client, b); err != nil {
			t.Fatalf("read %d: %v", n, err)
		}
		return b
	}

	if got := string(read(12)); got != "RFB 003.008\n" {
		t.Fatalf("version = %q", got)
	}
	client.Write([]byte("RFB 003.008\n"))
	if sec := read(2); sec[0] != 1 || sec[1] != 1 {
		t.Fatalf("security types = %v", sec)
	}
	client.Write([]byte{1}) // choose None
	read(4)                 // SecurityResult
	client.Write([]byte{1}) // ClientInit, shared

	init := read(24 + len("Orbit RMM"))
	if w := binary.BigEndian.Uint16(init[0:]); w != 4 {
		t.Fatalf("width = %d", w)
	}
	if init[4] != 32 || init[5] != 24 || init[7] != 1 {
		t.Fatalf("pixel format = %v", init[4:8])
	}

	// Request a frame; expect one raw rectangle of the whole screen.
	req := []byte{3, 0}
	req = binary.BigEndian.AppendUint16(req, 0)
	req = binary.BigEndian.AppendUint16(req, 0)
	req = binary.BigEndian.AppendUint16(req, 4)
	req = binary.BigEndian.AppendUint16(req, 2)
	client.Write(req)
	head := read(16)
	if head[0] != 0 || binary.BigEndian.Uint16(head[2:]) != 1 {
		t.Fatalf("update header = %v", head[:4])
	}
	if enc := binary.BigEndian.Uint32(head[12:]); enc != 0 {
		t.Fatalf("encoding = %d (want raw)", enc)
	}
	read(4 * 2 * 4) // the pixels

	// Pointer and key events reach the screen.
	ptr := []byte{5, 0b001}
	ptr = binary.BigEndian.AppendUint16(ptr, 3)
	ptr = binary.BigEndian.AppendUint16(ptr, 1)
	client.Write(ptr)
	key := []byte{4, 1, 0, 0}
	key = binary.BigEndian.AppendUint32(key, 0x61) // 'a'
	client.Write(key)

	deadline := time.Now().Add(2 * time.Second)
	var pointer [3]int
	var keySym uint32
	var keyDown bool
	for time.Now().Before(deadline) {
		screen.mu.Lock()
		pointer, keySym, keyDown = screen.pointer, screen.keySym, screen.keyDown
		screen.mu.Unlock()
		if pointer == [3]int{3, 1, 1} && keySym == 0x61 && keyDown {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pointer != [3]int{3, 1, 1} {
		t.Fatalf("pointer = %v", pointer)
	}
	if keySym != 0x61 || !keyDown {
		t.Fatalf("key = %#x down=%v", keySym, keyDown)
	}

	// End the session, then wait for serveVNC — holding no lock, so its
	// deferred screen.Close() can take the mutex.
	cancel()
	client.Close()
	if err := <-done; err != nil {
		t.Fatalf("serveVNC: %v", err)
	}
	screen.mu.Lock()
	closed := screen.closed
	screen.mu.Unlock()
	if !closed {
		t.Fatal("screen was not closed")
	}
}
