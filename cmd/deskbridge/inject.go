package main

// Control-injection seam shared across platforms. The OS-specific injectors live
// in inject_darwin.go (cgo/CGEvent), inject_linux.go (xdotool), and
// inject_other.go (unsupported stub). Everything here is build-tag free so the
// pure mapping/dispatch logic is always compiled and unit-testable without cgo.

import (
	"fmt"
	"os"
)

// controlInjector turns absolute-coordinate ControlEvents into real OS input.
// handle must be safe to call from a single goroutine (serveControlSink drives
// it serially). close releases any OS resources (event source, X display).
type controlInjector interface {
	handle(ev ControlEvent)
	close()
}

// absCoords maps a normalized 0..1 point onto an integer pixel/point coordinate
// within a display of the given width/height. Values are clamped to the display
// so a viewer that reports a coordinate slightly outside the canvas (rounding at
// the edges) cannot drive the cursor off-screen or produce a negative index.
func absCoords(nx, ny float64, w, h int) (int, int) {
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	x := int(clamp(nx) * float64(w))
	y := int(clamp(ny) * float64(h))
	// Keep the result strictly inside [0, w-1] / [0, h-1]; nx==1 would otherwise
	// land exactly on w (one past the last addressable pixel).
	if x >= w && w > 0 {
		x = w - 1
	}
	if y >= h && h > 0 {
		y = h - 1
	}
	return x, y
}

// clampQuality keeps an mjpeg -q:v value inside ffmpeg's 2 (best) .. 31 (worst)
// range. Shared by the capture-negotiation path.
func clampQuality(q int) int {
	if q < 2 {
		return 2
	}
	if q > 31 {
		return 31
	}
	return q
}

// modifierState tracks the latched modifier keys so mouse and key events can
// carry the current chord (e.g. Cmd+click, Shift+key). Both OS injectors embed
// it and update it from key events before applying the OS-specific flags.
type modifierState struct {
	shift, control, alt, meta bool
}

// update folds a key ControlEvent into the modifier state. It reports whether the
// event was itself a modifier key (callers may still forward it, but macOS in
// particular expresses modifiers purely through event flags).
func (m *modifierState) update(ev ControlEvent) (isModifier bool) {
	switch ev.Code {
	case "ShiftLeft", "ShiftRight":
		m.shift = ev.Down
		return true
	case "ControlLeft", "ControlRight":
		m.control = ev.Down
		return true
	case "AltLeft", "AltRight":
		m.alt = ev.Down
		return true
	case "MetaLeft", "MetaRight", "OSLeft", "OSRight":
		m.meta = ev.Down
		return true
	}
	return false
}

// logInjectorUnavailable prints a single clear message when no injector could be
// constructed, so a viewer's control events fail loudly instead of silently.
func logInjectorUnavailable(err error) {
	fmt.Fprintf(os.Stderr, "[control] input injection unavailable: %v (control events will be ignored)\n", err)
}
