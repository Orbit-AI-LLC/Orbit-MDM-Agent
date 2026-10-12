//go:build darwin

package remote

// Maps X11 keysyms (what RFB/VNC KeyEvents carry) to macOS virtual keycodes for a
// US QWERTY layout, used by Key in input_darwin.go. Printable ASCII keysyms equal
// their ASCII code; the 0xFFxx range is the special keys.

// letterKeycode is the virtual keycode of each lowercase letter.
var letterKeycode = map[byte]uint16{
	'a': 0, 'b': 11, 'c': 8, 'd': 2, 'e': 14, 'f': 3, 'g': 5, 'h': 4, 'i': 34,
	'j': 38, 'k': 40, 'l': 37, 'm': 46, 'n': 45, 'o': 31, 'p': 35, 'q': 12,
	'r': 15, 's': 1, 't': 17, 'u': 32, 'v': 9, 'w': 13, 'x': 7, 'y': 16, 'z': 6,
}

// digitKeycode is the virtual keycode of each digit.
var digitKeycode = map[byte]uint16{
	'0': 29, '1': 18, '2': 19, '3': 20, '4': 21, '5': 23, '6': 22, '7': 26, '8': 28, '9': 25,
}

// symbol maps a printable punctuation keysym to its keycode and whether it needs
// shift on a US layout. The unshifted and shifted characters of each physical key
// are both listed.
var symbolKeycode = map[byte]struct {
	code  uint16
	shift bool
}{
	' ': {49, false},
	'`': {50, false}, '~': {50, true},
	'-': {27, false}, '_': {27, true},
	'=': {24, false}, '+': {24, true},
	'[': {33, false}, '{': {33, true},
	']': {30, false}, '}': {30, true},
	'\\': {42, false}, '|': {42, true},
	';': {41, false}, ':': {41, true},
	'\'': {39, false}, '"': {39, true},
	',': {43, false}, '<': {43, true},
	'.': {47, false}, '>': {47, true},
	'/': {44, false}, '?': {44, true},
	'!': {18, true}, '@': {19, true}, '#': {20, true}, '$': {21, true},
	'%': {23, true}, '^': {22, true}, '&': {26, true}, '*': {28, true},
	'(': {25, true}, ')': {29, true},
}

// specialKeycode maps the 0xFFxx keysyms (navigation, editing, function keys).
var specialKeycode = map[uint32]uint16{
	0xFF08: 51,  // BackSpace
	0xFF09: 48,  // Tab
	0xFF0D: 36,  // Return
	0xFF1B: 53,  // Escape
	0xFFFF: 117, // Delete (forward)
	0xFF50: 115, // Home
	0xFF57: 119, // End
	0xFF55: 116, // Page Up
	0xFF56: 121, // Page Down
	0xFF51: 123, // Left
	0xFF52: 126, // Up
	0xFF53: 124, // Right
	0xFF54: 125, // Down
	0xFFBE: 122, 0xFFBF: 120, 0xFFC0: 99, 0xFFC1: 118, // F1-F4
	0xFFC2: 96, 0xFFC3: 97, 0xFFC4: 98, 0xFFC5: 100, // F5-F8
	0xFFC6: 101, 0xFFC7: 109, 0xFFC8: 103, 0xFFC9: 111, // F9-F12
}

// keysymToKeycode returns the macOS keycode for a keysym, whether it needs shift,
// and whether it is mapped at all.
func keysymToKeycode(sym uint32) (uint16, bool, bool) {
	if sym >= 'a' && sym <= 'z' {
		return letterKeycode[byte(sym)], false, true
	}
	if sym >= 'A' && sym <= 'Z' {
		return letterKeycode[byte(sym-'A'+'a')], true, true
	}
	if sym >= '0' && sym <= '9' {
		return digitKeycode[byte(sym)], false, true
	}
	if sym <= 0x7E {
		if s, ok := symbolKeycode[byte(sym)]; ok {
			return s.code, s.shift, true
		}
	}
	if code, ok := specialKeycode[sym]; ok {
		return code, false, true
	}
	return 0, false, false
}

// modifier maps a modifier keysym to its CGEvent flag mask and keycode.
func modifier(sym uint32) (flag uint64, code uint16, ok bool) {
	switch sym {
	case 0xFFE1: // Shift_L
		return cgFlagShift, 56, true
	case 0xFFE2: // Shift_R
		return cgFlagShift, 60, true
	case 0xFFE3: // Control_L
		return cgFlagControl, 59, true
	case 0xFFE4: // Control_R
		return cgFlagControl, 62, true
	case 0xFFE9, 0xFFE7: // Alt_L / Meta_L (Option)
		return cgFlagAlt, 58, true
	case 0xFFEA, 0xFFE8: // Alt_R / Meta_R
		return cgFlagAlt, 61, true
	case 0xFFEB, 0xFFE5: // Super_L (Command) / also map Caps-less combos
		return cgFlagCommand, 55, true
	case 0xFFEC: // Super_R
		return cgFlagCommand, 54, true
	}
	return 0, 0, false
}
