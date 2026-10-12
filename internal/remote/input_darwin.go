//go:build darwin

package remote

import (
	"sync"

	"github.com/ebitengine/purego"
)

// input_darwin.go injects mouse and keyboard events for the remote desktop's
// keyboard-and-mouse control, and raises the Accessibility prompt. It calls
// CoreGraphics and ApplicationServices through purego (dlopen at runtime), so the
// agent keeps building with CGO_ENABLED=0 and still cross-compiles from one host.
//
// It runs in the desktop helper, the per-user GUI-session process — the only one
// macOS lets hold Accessibility (which CGEventPost requires) and have it granted
// without an MDM. Until Accessibility is granted, macOS silently drops the posted
// events, which is why the helper prompts for it (promptAccessibility).

// CoreGraphics event types and constants.
const (
	cgEventLeftMouseDown  = 1
	cgEventLeftMouseUp    = 2
	cgEventRightMouseDown = 3
	cgEventRightMouseUp   = 4
	cgEventMouseMoved     = 5
	cgEventLeftMouseDrag  = 6
	cgEventRightMouseDrag = 7
	cgEventOtherMouseDown = 25
	cgEventOtherMouseUp   = 26
	cgEventOtherMouseDrag = 27

	cgMouseButtonLeft   = 0
	cgMouseButtonRight  = 1
	cgMouseButtonCenter = 2

	cgHIDEventTap = 0

	cgFlagShift   = 0x00020000
	cgFlagControl = 0x00040000
	cgFlagAlt     = 0x00080000
	cgFlagCommand = 0x00100000
)

type cgPoint struct{ X, Y float64 }

var input struct {
	once sync.Once
	ok   bool

	createMouse func(source uintptr, evType uint32, pos cgPoint, button uint32) uintptr
	createKey   func(source uintptr, keycode uint16, down bool) uintptr
	setFlags    func(event uintptr, flags uint64)
	post        func(tap uint32, event uintptr)
	release     func(ref uintptr)

	mainDisplay func() uint32
	copyMode    func(display uint32) uintptr
	modeWidth   func(mode uintptr) uint64
	modeHeight  func(mode uintptr) uint64
	modeRelease func(mode uintptr)

	axTrusted func() bool
}

func initInput() {
	input.once.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		as, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&input.createMouse, cg, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&input.createKey, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&input.setFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&input.post, cg, "CGEventPost")
		purego.RegisterLibFunc(&input.release, cf, "CFRelease")
		purego.RegisterLibFunc(&input.mainDisplay, cg, "CGMainDisplayID")
		purego.RegisterLibFunc(&input.copyMode, cg, "CGDisplayCopyDisplayMode")
		purego.RegisterLibFunc(&input.modeWidth, cg, "CGDisplayModeGetWidth")
		purego.RegisterLibFunc(&input.modeHeight, cg, "CGDisplayModeGetHeight")
		purego.RegisterLibFunc(&input.modeRelease, cg, "CGDisplayModeRelease")
		purego.RegisterLibFunc(&input.axTrusted, as, "AXIsProcessTrusted")
		input.ok = input.createMouse != nil && input.post != nil && input.createKey != nil
	})
}

// displayPoints is the main display's size in points (the coordinate space
// CGEvent uses), which differs from the captured pixel size on a Retina display.
// Zero,zero when it can't be read.
func displayPoints() (w, h float64) {
	initInput()
	if input.mainDisplay == nil || input.copyMode == nil {
		return 0, 0
	}
	mode := input.copyMode(input.mainDisplay())
	if mode == 0 {
		return 0, 0
	}
	defer input.modeRelease(mode)
	return float64(input.modeWidth(mode)), float64(input.modeHeight(mode))
}

// accessibilityTrusted reports whether this process may inject events. Calling it
// also lists the agent under System Settings → Privacy → Accessibility (as off),
// so the person can turn remote control on themselves — the menu-bar app's "Fix…"
// opens that pane. No MDM needed; it works the same way Screen Recording does.
func accessibilityTrusted() bool {
	initInput()
	if input.axTrusted == nil {
		return false
	}
	return input.axTrusted()
}

// requestAccessibility lists the agent under Accessibility so remote control can
// be granted without an MDM. Safe to call repeatedly; a no-op once granted.
func requestAccessibility() { accessibilityTrusted() }

// postMouse builds and posts one mouse event at the given point (in display
// points), with the session's current modifier flags.
func postMouse(evType uint32, x, y float64, button uint32, flags uint64) {
	if !input.ok {
		return
	}
	ev := input.createMouse(0, evType, cgPoint{X: x, Y: y}, button)
	if ev == 0 {
		return
	}
	if flags != 0 {
		input.setFlags(ev, flags)
	}
	input.post(cgHIDEventTap, ev)
	input.release(ev)
}

// postKey builds and posts one key event with the given modifier flags.
func postKey(keycode uint16, down bool, flags uint64) {
	if !input.ok {
		return
	}
	ev := input.createKey(0, keycode, down)
	if ev == 0 {
		return
	}
	if flags != 0 {
		input.setFlags(ev, flags)
	}
	input.post(cgHIDEventTap, ev)
	input.release(ev)
}

// Pointer moves the cursor and presses/releases buttons for a VNC PointerEvent.
// x,y are in the served (downscaled) framebuffer; they're scaled to display
// points. buttons is the RFB mask (bit 0 left, 1 middle, 2 right).
func (s *macScreen) Pointer(x, y int, buttons uint8) {
	initInput()
	if !input.ok {
		return
	}
	px, py := s.toPoints(x, y)
	old := s.lastButtons
	s.lastButtons = buttons
	if changed := old ^ buttons; changed != 0 {
		for _, b := range []struct {
			mask       uint8
			down, up   uint32
			cgButton   uint32
		}{
			{1, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft},
			{4, cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight},
			{2, cgEventOtherMouseDown, cgEventOtherMouseUp, cgMouseButtonCenter},
		} {
			if changed&b.mask == 0 {
				continue
			}
			if buttons&b.mask != 0 {
				postMouse(b.down, px, py, b.cgButton, s.mods)
			} else {
				postMouse(b.up, px, py, b.cgButton, s.mods)
			}
		}
		return
	}
	// No button change: a move, or a drag while a button is held.
	switch {
	case buttons&1 != 0:
		postMouse(cgEventLeftMouseDrag, px, py, cgMouseButtonLeft, s.mods)
	case buttons&4 != 0:
		postMouse(cgEventRightMouseDrag, px, py, cgMouseButtonRight, s.mods)
	case buttons&2 != 0:
		postMouse(cgEventOtherMouseDrag, px, py, cgMouseButtonCenter, s.mods)
	default:
		postMouse(cgEventMouseMoved, px, py, cgMouseButtonLeft, s.mods)
	}
}

// toPoints scales a served-framebuffer coordinate to display points.
func (s *macScreen) toPoints(x, y int) (float64, float64) {
	pw, ph := s.pointW, s.pointH
	if pw <= 0 || ph <= 0 || s.w == 0 || s.h == 0 {
		return float64(x), float64(y)
	}
	return float64(x) * pw / float64(s.w), float64(y) * ph / float64(s.h)
}

// Key presses or releases a key for a VNC KeyEvent, by X11 keysym. Modifier
// keysyms update the session's flag state (and post a real modifier event so held
// combinations work); other keys post with the current flags, adding shift for a
// keysym that needs it on a US layout.
func (s *macScreen) Key(sym uint32, down bool) {
	initInput()
	if !input.ok {
		return
	}
	if flag, code, ok := modifier(sym); ok {
		if down {
			s.mods |= flag
		} else {
			s.mods &^= flag
		}
		postKey(code, down, s.mods)
		return
	}
	code, shift, ok := keysymToKeycode(sym)
	if !ok {
		return
	}
	flags := s.mods
	if shift {
		flags |= cgFlagShift
	}
	postKey(code, down, flags)
}
