//go:build darwin

package main

// macOS input injection via CoreGraphics quartz events (CGEvent). This runs on
// the CAPTURER side: it receives normalized ControlEvents from the remote viewer
// and posts real mouse/keyboard events into the local session.
//
// Coordinates arrive normalized 0..1 against the captured frame; because the
// capture is the full main display, normalized coords map directly onto the
// display's global coordinate space (points, top-left origin) via CGDisplayBounds.
//
// NOTE: posting events requires Accessibility permission (System Settings >
// Privacy & Security > Accessibility) for whatever process runs the capturer.
// Without it CGEventPost is silently dropped by the OS; we cannot detect that
// from here, so the caller should surface a setup hint out-of-band.

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>

static void dbMainDisplaySize(double *w, double *h) {
    CGRect b = CGDisplayBounds(CGMainDisplayID());
    *w = b.size.width;
    *h = b.size.height;
}

// Post a mouse move (or drag, when a button is held) to an absolute point.
static void dbMouseMove(double x, double y, int leftDown, int rightDown, int otherDown, uint64_t flags) {
    CGEventType t = kCGEventMouseMoved;
    CGMouseButton btn = kCGMouseButtonLeft;
    if (leftDown) { t = kCGEventLeftMouseDragged; btn = kCGMouseButtonLeft; }
    else if (rightDown) { t = kCGEventRightMouseDragged; btn = kCGMouseButtonRight; }
    else if (otherDown) { t = kCGEventOtherMouseDragged; btn = kCGMouseButtonCenter; }
    CGEventRef e = CGEventCreateMouseEvent(NULL, t, CGPointMake(x, y), btn);
    if (e) {
        CGEventSetFlags(e, (CGEventFlags)flags);
        CGEventPost(kCGHIDEventTap, e);
        CFRelease(e);
    }
}

// Post a mouse button press/release. button: 0=left,1=middle,2=right.
static void dbMouseButton(double x, double y, int button, int down, int clicks, uint64_t flags) {
    CGEventType t;
    CGMouseButton btn;
    switch (button) {
        case 2: t = down ? kCGEventRightMouseDown : kCGEventRightMouseUp; btn = kCGMouseButtonRight; break;
        case 1: t = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp; btn = kCGMouseButtonCenter; break;
        default: t = down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp; btn = kCGMouseButtonLeft; break;
    }
    CGEventRef e = CGEventCreateMouseEvent(NULL, t, CGPointMake(x, y), btn);
    if (e) {
        if (clicks > 1) CGEventSetIntegerValueField(e, kCGMouseEventClickState, clicks);
        CGEventSetFlags(e, (CGEventFlags)flags);
        CGEventPost(kCGHIDEventTap, e);
        CFRelease(e);
    }
}

// Post a scroll wheel event. Pixel units; wheel1 = vertical, wheel2 = horizontal.
static void dbScroll(int dy, int dx) {
    CGEventRef e = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitPixel, 2, dy, dx);
    if (e) {
        CGEventPost(kCGHIDEventTap, e);
        CFRelease(e);
    }
}

// Post a keyboard key down/up for a CGKeyCode, carrying modifier flags.
static void dbKey(int keycode, int down, uint64_t flags) {
    CGEventRef e = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, down ? true : false);
    if (e) {
        CGEventSetFlags(e, (CGEventFlags)flags);
        CGEventPost(kCGHIDEventTap, e);
        CFRelease(e);
    }
}
*/
import "C"

// CGEventFlags modifier masks (from CGEventTypes.h).
const (
	cgFlagShift   = 1 << 17
	cgFlagControl = 1 << 18
	cgFlagAlt     = 1 << 19
	cgFlagCommand = 1 << 20
)

type macInjector struct {
	w, h               float64
	left, right, other bool
	mods               modifierState
}

func newInjector() (controlInjector, error) {
	var w, h C.double
	C.dbMainDisplaySize(&w, &h)
	inj := &macInjector{w: float64(w), h: float64(h)}
	if inj.w <= 0 || inj.h <= 0 {
		// Fall back to a sane default rather than dividing into a zero display.
		inj.w, inj.h = 1440, 900
	}
	return inj, nil
}

func (m *macInjector) flags() C.uint64_t {
	var f C.uint64_t
	if m.mods.shift {
		f |= cgFlagShift
	}
	if m.mods.control {
		f |= cgFlagControl
	}
	if m.mods.alt {
		f |= cgFlagAlt
	}
	if m.mods.meta {
		f |= cgFlagCommand
	}
	return f
}

func (m *macInjector) point(nx, ny float64) (C.double, C.double) {
	x, y := absCoords(nx, ny, int(m.w), int(m.h))
	return C.double(x), C.double(y)
}

func (m *macInjector) handle(ev ControlEvent) {
	switch ev.Type {
	case "mouse-move":
		x, y := m.point(ev.X, ev.Y)
		C.dbMouseMove(x, y, b2i(m.left), b2i(m.right), b2i(m.other), m.flags())
	case "mouse-button":
		x, y := m.point(ev.X, ev.Y)
		switch ev.Button {
		case 2:
			m.right = ev.Down
		case 1:
			m.other = ev.Down
		default:
			m.left = ev.Down
		}
		C.dbMouseButton(x, y, C.int(ev.Button), b2i(ev.Down), 1, m.flags())
	case "scroll":
		// JS wheel deltas are "content moves down = positive"; CG scroll is the
		// opposite sign, so invert to match natural direction.
		C.dbScroll(C.int(-int(ev.DY)), C.int(-int(ev.DX)))
	case "key":
		m.mods.update(ev)
		if code, ok := macKeyCode(ev.Code, ev.Key); ok {
			C.dbKey(C.int(code), b2i(ev.Down), m.flags())
		}
	}
}

func (m *macInjector) close() {}

func b2i(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// macKeyCode maps a browser KeyboardEvent.code (preferred, layout-independent)
// or .key to a macOS virtual keycode (kVK_*). Returns false for keys we do not
// map (they are simply dropped in Phase 1).
func macKeyCode(code, key string) (int, bool) {
	if kc, ok := macCodeMap[code]; ok {
		return kc, true
	}
	// Fall back to single-character .key for letters/digits when .code is absent.
	if len(key) == 1 {
		if kc, ok := macCodeMap["Key"+upper1(key)]; ok {
			return kc, true
		}
	}
	return 0, false
}

func upper1(s string) string {
	if len(s) == 1 && s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0] - 32)
	}
	return s
}

// macCodeMap covers the common keys. Values are kVK_* virtual keycodes.
var macCodeMap = map[string]int{
	"KeyA": 0, "KeyS": 1, "KeyD": 2, "KeyF": 3, "KeyH": 4, "KeyG": 5, "KeyZ": 6,
	"KeyX": 7, "KeyC": 8, "KeyV": 9, "KeyB": 11, "KeyQ": 12, "KeyW": 13, "KeyE": 14,
	"KeyR": 15, "KeyY": 16, "KeyT": 17, "KeyO": 31, "KeyU": 32, "KeyI": 34, "KeyP": 35,
	"KeyL": 37, "KeyJ": 38, "KeyK": 40, "KeyN": 45, "KeyM": 46,
	"Digit1": 18, "Digit2": 19, "Digit3": 20, "Digit4": 21, "Digit6": 22, "Digit5": 23,
	"Digit9": 25, "Digit7": 26, "Digit8": 28, "Digit0": 29,
	"Equal": 24, "Minus": 27, "BracketRight": 30, "BracketLeft": 33,
	"Quote": 39, "Semicolon": 41, "Backslash": 42, "Comma": 43, "Slash": 44,
	"Period": 47, "Backquote": 50,
	"Return": 36, "Enter": 36, "Tab": 48, "Space": 49, "Backspace": 51, "Delete": 51,
	"Escape": 53, "ForwardDelete": 117,
	"ArrowLeft": 123, "ArrowRight": 124, "ArrowDown": 125, "ArrowUp": 126,
	"Home": 115, "End": 119, "PageUp": 116, "PageDown": 121,
	"F1": 122, "F2": 120, "F3": 99, "F4": 118, "F5": 96, "F6": 97, "F7": 98,
	"F8": 100, "F9": 101, "F10": 109, "F11": 103, "F12": 111,
	// Modifiers still get a keycode for completeness; flags carry the real state.
	"ShiftLeft": 56, "ShiftRight": 60, "ControlLeft": 59, "ControlRight": 62,
	"AltLeft": 58, "AltRight": 61, "MetaLeft": 55, "MetaRight": 54, "CapsLock": 57,
}
