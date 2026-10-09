//go:build darwin

package remote

import (
	"context"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// The screen is captured at most this wide or tall; a Retina display is far more
// than a remote session needs, and smaller frames keep the link light.
const macMaxDimension = 1440

// macScreen captures the Mac's display with the built-in `screencapture` tool —
// no third-party VNC server, no screen sharing to turn on. It needs the Screen
// Recording permission, which an MDM can grant the agent with a PPPC profile.
//
// Input (pointer and keyboard) is not injected yet, so the session is
// view-only; control is the next step (it needs the CoreGraphics event bridge).
type macScreen struct {
	w, h     int // the framebuffer size served to the client (downscaled)
	srcW     int
	srcH     int
	tmp      string
	captures sync.Mutex
}

// newMacScreen captures one frame to learn the display size and lock the served
// dimensions for the session.
func newMacScreen() (*macScreen, error) {
	dir, err := os.MkdirTemp("", "orbit-vnc-")
	if err != nil {
		return nil, err
	}
	s := &macScreen{tmp: filepath.Join(dir, "frame.png")}
	img, err := s.grab()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	b := img.Bounds()
	s.srcW, s.srcH = b.Dx(), b.Dy()
	s.w, s.h = fitWithin(s.srcW, s.srcH, macMaxDimension)
	return s, nil
}

func (s *macScreen) Size() (int, int) { return s.w, s.h }

// grab captures the main display to a PNG and decodes it.
func (s *macScreen) grab() (image.Image, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// -x no sound, -C include the cursor, -t png, -D 1 the main display.
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-C", "-t", "png", "-D", "1", s.tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("screencapture failed (the agent may lack Screen Recording permission): %v: %s", err, out)
	}
	defer os.Remove(s.tmp)
	file, err := os.Open(s.tmp)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	return img, err
}

// Frame captures the screen and returns it downscaled to the locked size as
// blue, green, red, unused bytes.
func (s *macScreen) Frame() (int, int, []byte, error) {
	s.captures.Lock()
	defer s.captures.Unlock()
	img, err := s.grab()
	if err != nil {
		return 0, 0, nil, err
	}
	return s.w, s.h, toBGRX(img, s.w, s.h), nil
}

// Input isn't injected yet: the session is view-only.
func (s *macScreen) Pointer(int, int, uint8) {}
func (s *macScreen) Key(uint32, bool)        {}

func (s *macScreen) Close() {
	if s.tmp != "" {
		os.RemoveAll(filepath.Dir(s.tmp))
	}
}

// builtInScreen captures this Mac's display.
func builtInScreen() (Screen, error) { return newMacScreen() }
