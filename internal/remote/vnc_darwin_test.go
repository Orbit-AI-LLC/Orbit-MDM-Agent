//go:build darwin

package remote

import "testing"

// TestBuiltInCapture is a smoke test on a real Mac: it captures one frame. It
// skips when the OS denies capture (no Screen Recording permission, or a
// headless CI runner), so it never fails the build for that reason.
func TestBuiltInCapture(t *testing.T) {
	screen, err := builtInScreen()
	if err != nil {
		t.Skipf("no screen capture here: %v", err)
	}
	defer screen.Close()

	w, h := screen.Size()
	if w <= 0 || h <= 0 || w > macMaxDimension || h > macMaxDimension {
		t.Fatalf("size = %dx%d", w, h)
	}
	fw, fh, pixels, err := screen.Frame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if fw != w || fh != h {
		t.Fatalf("frame size %dx%d != %dx%d", fw, fh, w, h)
	}
	if len(pixels) != w*h*4 {
		t.Fatalf("pixels = %d, want %d", len(pixels), w*h*4)
	}
}
