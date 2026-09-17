//go:build linux

package main

// Linux input injection by shelling to xdotool. This avoids a cgo dependency on
// libXtst/libX11 headers at build time (the binary stays statically simple) while
// still delivering real mouse/keyboard injection on any X11 session that has
// xdotool installed. If xdotool is missing, newInjector fails with a clear
// message and control events are ignored rather than crashing.
//
// Normalized 0..1 coords map onto the display geometry reported by
// `xdotool getdisplaygeometry`. Wayland sessions are not supported by xdotool;
// that limitation is inherent to Phase 1.

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

type xdoInjector struct {
	bin                string
	w, h               int
	left, right, other bool
	mods               modifierState
}

func newInjector() (controlInjector, error) {
	bin, err := exec.LookPath("xdotool")
	if err != nil {
		return nil, errors.New("xdotool was not found in PATH; install xdotool for remote control on Linux")
	}
	inj := &xdoInjector{bin: bin, w: 1920, h: 1080}
	if out, err := exec.Command(bin, "getdisplaygeometry").Output(); err == nil {
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) == 2 {
			if w, err := strconv.Atoi(parts[0]); err == nil && w > 0 {
				inj.w = w
			}
			if h, err := strconv.Atoi(parts[1]); err == nil && h > 0 {
				inj.h = h
			}
		}
	}
	return inj, nil
}

func (x *xdoInjector) run(args ...string) {
	// Best-effort: injection failures should not tear down the control channel.
	_ = exec.Command(x.bin, args...).Run()
}

func (x *xdoInjector) handle(ev ControlEvent) {
	switch ev.Type {
	case "mouse-move":
		px, py := absCoords(ev.X, ev.Y, x.w, x.h)
		x.run("mousemove", strconv.Itoa(px), strconv.Itoa(py))
	case "mouse-button":
		px, py := absCoords(ev.X, ev.Y, x.w, x.h)
		x.run("mousemove", strconv.Itoa(px), strconv.Itoa(py))
		btn := xdoButton(ev.Button)
		switch ev.Button {
		case 2:
			x.right = ev.Down
		case 1:
			x.other = ev.Down
		default:
			x.left = ev.Down
		}
		if ev.Down {
			x.run("mousedown", btn)
		} else {
			x.run("mouseup", btn)
		}
	case "scroll":
		// X buttons 4/5 = vertical, 6/7 = horizontal wheel clicks.
		if ev.DY != 0 {
			btn := "5" // down
			if ev.DY < 0 {
				btn = "4" // up
			}
			x.run("click", btn)
		}
		if ev.DX != 0 {
			btn := "7" // right
			if ev.DX < 0 {
				btn = "6" // left
			}
			x.run("click", btn)
		}
	case "key":
		x.mods.update(ev)
		if sym, ok := xKeysym(ev.Code, ev.Key); ok {
			if ev.Down {
				x.run("keydown", sym)
			} else {
				x.run("keyup", sym)
			}
		}
	}
}

func (x *xdoInjector) close() {}

func xdoButton(button int) string {
	switch button {
	case 2:
		return "3" // right
	case 1:
		return "2" // middle
	default:
		return "1" // left
	}
}

// xKeysym maps a browser KeyboardEvent.code/.key to an X11 keysym name accepted
// by `xdotool key`. Returns false for unmapped keys.
func xKeysym(code, key string) (string, bool) {
	if sym, ok := xCodeMap[code]; ok {
		return sym, true
	}
	// Printable single characters pass through as their own keysym (xdotool
	// accepts e.g. "a", "A", "1", "@").
	if len(key) == 1 && key[0] > 0x20 && key[0] < 0x7f {
		return key, true
	}
	return "", false
}

var xCodeMap = map[string]string{
	"Enter": "Return", "Return": "Return", "Tab": "Tab", "Space": "space",
	"Backspace": "BackSpace", "Delete": "Delete", "Escape": "Escape",
	"ArrowLeft": "Left", "ArrowRight": "Right", "ArrowUp": "Up", "ArrowDown": "Down",
	"Home": "Home", "End": "End", "PageUp": "Prior", "PageDown": "Next",
	"ShiftLeft": "Shift_L", "ShiftRight": "Shift_R",
	"ControlLeft": "Control_L", "ControlRight": "Control_R",
	"AltLeft": "Alt_L", "AltRight": "Alt_R",
	"MetaLeft": "Super_L", "MetaRight": "Super_R", "CapsLock": "Caps_Lock",
	"F1": "F1", "F2": "F2", "F3": "F3", "F4": "F4", "F5": "F5", "F6": "F6",
	"F7": "F7", "F8": "F8", "F9": "F9", "F10": "F10", "F11": "F11", "F12": "F12",
}
