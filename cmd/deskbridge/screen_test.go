package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"testing"
	"time"
)

// TestWriteReadFrameRoundTrip covers small and large (multi-chunk) payloads.
func TestWriteReadFrameRoundTrip(t *testing.T) {
	sizes := []int{1, 100, screenChunkBytes - 1, screenChunkBytes, screenChunkBytes + 1, 3*screenChunkBytes + 7}
	for _, n := range sizes {
		payload := make([]byte, n)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := writeFrame(&buf, payload); err != nil {
			t.Fatalf("writeFrame(%d): %v", n, err)
		}
		got, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("readFrame(%d): %v", n, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload mismatch at size %d", n)
		}
		if buf.Len() != 0 {
			t.Fatalf("size %d: %d trailing bytes", n, buf.Len())
		}
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // 4GB length header
	if _, err := readFrame(&buf); err == nil {
		t.Fatal("expected oversize frame to be rejected")
	}
}

// TestSplitMJPEG feeds three concatenated real JPEGs and expects three frames,
// each decodable, in order.
func TestSplitMJPEG(t *testing.T) {
	var stream bytes.Buffer
	want := 3
	for i := 0; i < want; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		img.Set(i, i, color.RGBA{R: uint8(i * 40), A: 255})
		if err := jpeg.Encode(&stream, img, &jpeg.Options{Quality: 80}); err != nil {
			t.Fatal(err)
		}
	}
	var frames [][]byte
	if err := splitMJPEG(bytes.NewReader(stream.Bytes()), func(f []byte) error {
		frames = append(frames, f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != want {
		t.Fatalf("got %d frames, want %d", len(frames), want)
	}
	for i, f := range frames {
		if _, err := jpeg.Decode(bytes.NewReader(f)); err != nil {
			t.Fatalf("frame %d not a valid JPEG: %v", i, err)
		}
	}
}

// TestScreenPipeLoopback proves the full split->frame->deframe path over a real
// TCP loopback, independent of ffmpeg and the relay: a producer splits a
// concatenated-JPEG stream and frames it; a consumer deframes and validates.
func TestScreenPipeLoopback(t *testing.T) {
	var mjpeg bytes.Buffer
	const want = 4
	for i := 0; i < want; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 32, 24))
		img.Set(i, 0, color.RGBA{G: 255, A: 255})
		if err := jpeg.Encode(&mjpeg, img, &jpeg.Options{Quality: 75}); err != nil {
			t.Fatal(err)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer conn.Close()
		_ = splitMJPEG(bytes.NewReader(mjpeg.Bytes()), func(f []byte) error {
			return writeFrame(conn, f)
		})
	}()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	got := 0
	err = readScreenFrames(conn, func(f []byte) error {
		if _, decErr := jpeg.Decode(bytes.NewReader(f)); decErr != nil {
			return decErr
		}
		got++
		if got >= want {
			return io.EOF
		}
		return nil
	})
	if err != nil && err != io.EOF {
		t.Fatalf("readScreenFrames: %v", err)
	}
	if got != want {
		t.Fatalf("received %d frames, want %d", got, want)
	}
}

// TestAbsCoords verifies normalized->absolute mapping, including clamping at the
// edges so nx/ny == 1 stays inside the display and out-of-range values are pinned.
func TestAbsCoords(t *testing.T) {
	cases := []struct {
		nx, ny float64
		w, h   int
		wantX  int
		wantY  int
	}{
		{0, 0, 1920, 1080, 0, 0},
		{0.5, 0.5, 1920, 1080, 960, 540},
		{1, 1, 1920, 1080, 1919, 1079}, // last addressable pixel, not one past
		{0.25, 0.75, 1000, 800, 250, 600},
		{-0.5, 2.0, 1280, 720, 0, 719}, // clamped below 0 and above 1
		{0.999, 0.999, 100, 100, 99, 99},
	}
	for _, c := range cases {
		x, y := absCoords(c.nx, c.ny, c.w, c.h)
		if x != c.wantX || y != c.wantY {
			t.Errorf("absCoords(%v,%v,%d,%d)=(%d,%d), want (%d,%d)", c.nx, c.ny, c.w, c.h, x, y, c.wantX, c.wantY)
		}
	}
}

// TestNegotiateCapture checks the capture-negotiation header parsing: a valid
// header overrides defaults (with quality clamped), and absent/malformed/empty
// input falls back to defaults so non-negotiating viewers keep working.
func TestNegotiateCapture(t *testing.T) {
	def := defaultCaptureOptions()

	// Valid header with an out-of-range quality that must be clamped to 31.
	var buf bytes.Buffer
	if err := writeFrame(&buf, marshalNegotiation(streamNegotiation{Width: 800, FPS: 20, Quality: 99})); err != nil {
		t.Fatal(err)
	}
	got := negotiateCapture(&buf)
	if got.width != 800 || got.fps != 20 || got.quality != 31 {
		t.Fatalf("negotiated=%+v, want width=800 fps=20 quality=31", got)
	}

	// Zero fields keep the defaults.
	buf.Reset()
	if err := writeFrame(&buf, marshalNegotiation(streamNegotiation{Width: 640})); err != nil {
		t.Fatal(err)
	}
	got = negotiateCapture(&buf)
	if got.width != 640 || got.fps != def.fps || got.quality != def.quality {
		t.Fatalf("partial negotiated=%+v, want width=640 with default fps/quality", got)
	}

	// No header at all (EOF) -> defaults.
	empty := bytes.NewReader(nil)
	if got := negotiateCapture(empty); got != def {
		t.Fatalf("empty negotiated=%+v, want defaults %+v", got, def)
	}

	// Malformed JSON frame -> defaults.
	buf.Reset()
	if err := writeFrame(&buf, []byte("not-json")); err != nil {
		t.Fatal(err)
	}
	if got := negotiateCapture(&buf); got != def {
		t.Fatalf("malformed negotiated=%+v, want defaults %+v", got, def)
	}
}

// TestModifierState confirms modifier key events latch/clear the chord state.
func TestModifierState(t *testing.T) {
	var m modifierState
	if !m.update(ControlEvent{Type: "key", Code: "ShiftLeft", Down: true}) {
		t.Fatal("ShiftLeft should be reported as a modifier")
	}
	if !m.shift {
		t.Fatal("shift should be latched on")
	}
	if m.update(ControlEvent{Type: "key", Code: "KeyA", Down: true}) {
		t.Fatal("KeyA is not a modifier")
	}
	m.update(ControlEvent{Type: "key", Code: "ShiftLeft", Down: false})
	if m.shift {
		t.Fatal("shift should clear on release")
	}
	m.update(ControlEvent{Type: "key", Code: "MetaLeft", Down: true})
	m.update(ControlEvent{Type: "key", Code: "ControlRight", Down: true})
	if !m.meta || !m.control {
		t.Fatal("meta+control should both latch")
	}
}

// TestControlSinkDecodesEvents confirms framed ControlEvents deframe and decode.
func TestControlSinkDecodesEvents(t *testing.T) {
	events := []ControlEvent{
		{Type: "mouse-move", X: 0.5, Y: 0.25},
		{Type: "mouse-button", Button: 0, Down: true, X: 0.1, Y: 0.9},
		{Type: "key", Key: "a", Code: "KeyA", Down: true},
	}
	var buf bytes.Buffer
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFrame(&buf, data); err != nil {
			t.Fatal(err)
		}
	}
	got := 0
	err := readScreenFrames(&buf, func(f []byte) error {
		var ev ControlEvent
		if err := json.Unmarshal(f, &ev); err != nil {
			return err
		}
		got++
		return nil
	})
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if got != len(events) {
		t.Fatalf("decoded %d events, want %d", got, len(events))
	}
}
