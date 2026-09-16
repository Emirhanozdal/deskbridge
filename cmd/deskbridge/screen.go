package main

// Remote-desktop spike (Phase 0 + Phase 1 groundwork).
//
// This file implements the video/control pipe that rides the EXISTING inner
// mutual-TLS + yamux tunnel established in relay.go. It is intentionally
// additive: it does not change the file (service 1) or KVM/Deskflow (service 2)
// behavior. Two new yamux service ids are introduced in relay.go:
//
//	service 3 = "screen"  : the viewer opens the stream, writes [3, role], and
//	                        the peer (capturer) pushes MJPEG frames back.
//	service 4 = "control" : the viewer opens the stream, writes [4, role], and
//	                        streams absolute mouse/keyboard events to the peer.
//
// The yamux stream is a raw byte pipe with no message boundaries, so every
// logical unit on services 3/4 is length-prefixed: [u32 big-endian length]
// followed by that many payload bytes (writeFrame / readFrame). Individual
// Write calls to the tunnel are chunked to <= screenChunkBytes so we never hand
// yamux/TLS/the Cloudflare Worker a payload larger than they like (TLS records
// are 16KB; the Worker caps a WS frame at 1MB).
//
// Phase 0 capture is pragmatic: we shell out to ffmpeg to produce a raw MJPEG
// byte stream (avfoundation on macOS, x11grab on Linux), split it into
// individual JPEGs, and frame each one onto the stream. If ffmpeg is missing we
// fail with a clear message rather than bundling it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// yamux service ids (services 1 and 2 live in relay.go).
const (
	screenService  byte = 3
	controlService byte = 4
)

// role selector byte written right after the service id on services 3/4. Only
// the viewer initiates today; the byte reserves space for future roles
// (e.g. request a specific display, or a "capturer initiates" push mode).
const (
	roleViewer byte = 0
)

// Local loopback seam, mirroring the existing 47890 (files) / 24801 (kvm) ports.
// The relay exposes these on the viewer side; browser/native viewers connect to
// them and speak the raw [u32][payload] framing.
const (
	screenViewPort    = 47893 // viewer reads framed MJPEG here
	viewerBridgePort  = 47894 // WebSocket/HTTP bridge for the browser viewer
	controlInputPort  = 47895 // viewer writes framed control events here
	screenChunkBytes  = 256 * 1024
	screenMaxFrameLen = 16 << 20 // 16MB hard ceiling; a sane JPEG never approaches this
)

// captureOptions are the bandwidth/quality knobs for the ffmpeg capture.
type captureOptions struct {
	width   int    // downscale target width in px; 0 = native resolution
	fps     int    // capture frame rate
	quality int    // mjpeg -q:v, 2 (best) .. 31 (worst)
	device  string // capture device override (avfoundation index/name or X11 display)
}

func defaultCaptureOptions() captureOptions {
	return captureOptions{width: 1280, fps: 12, quality: 7}
}

// writeFrame writes one length-prefixed payload. The u32 length covers the whole
// payload, but the payload bytes are handed to w in <= screenChunkBytes slices so
// no single Write to the tunnel exceeds the safe record/frame size.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > screenMaxFrameLen {
		return fmt.Errorf("frame of %d bytes exceeds %d limit", len(payload), screenMaxFrameLen)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for off := 0; off < len(payload); {
		end := off + screenChunkBytes
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := w.Write(payload[off:end]); err != nil {
			return err
		}
		off = end
	}
	return nil
}

// readFrame reads one length-prefixed payload written by writeFrame.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, errors.New("empty frame")
	}
	if n > screenMaxFrameLen {
		return nil, fmt.Errorf("frame length %d exceeds %d limit", n, screenMaxFrameLen)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// splitMJPEG scans a raw concatenated-JPEG (MJPEG) byte stream and calls emit
// once per complete JPEG image (SOI 0xFFD8 .. EOI 0xFFD9). For baseline MJPEG
// the only bare 0xFFD9 is the real end-of-image marker (in-image 0xFF bytes are
// stuffed with 0x00 or are restart markers 0xD0-0xD7), so this segmentation is
// reliable for the ffmpeg mjpeg encoder output we produce. It returns nil on a
// clean EOF.
func splitMJPEG(r io.Reader, emit func([]byte) error) error {
	br := bufio.NewReaderSize(r, 1<<20)
	var buf []byte
	inFrame := false
	var prev byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !inFrame {
			if prev == 0xFF && b == 0xD8 {
				inFrame = true
				buf = []byte{0xFF, 0xD8}
			}
			prev = b
			continue
		}
		buf = append(buf, b)
		if prev == 0xFF && b == 0xD9 {
			frame := make([]byte, len(buf))
			copy(frame, buf)
			if err := emit(frame); err != nil {
				return err
			}
			inFrame = false
			buf = nil
			prev = 0
			continue
		}
		prev = b
	}
}

// captureArgs builds the OS-specific ffmpeg argument list that emits MJPEG on
// stdout. macOS uses avfoundation screen capture; Linux uses x11grab.
func captureArgs(opts captureOptions) ([]string, error) {
	fps := opts.fps
	if fps <= 0 {
		fps = 10
	}
	q := opts.quality
	if q <= 0 {
		q = 7
	}
	// yuvj420p keeps the mjpeg encoder happy across ffmpeg builds; add a scale
	// filter (even dimensions via -2) only when downscaling is requested.
	vf := "format=yuvj420p"
	if opts.width > 0 {
		vf = fmt.Sprintf("scale=%d:-2,format=yuvj420p", opts.width)
	}
	tail := []string{"-vf", vf, "-c:v", "mjpeg", "-q:v", strconv.Itoa(q), "-f", "mjpeg", "-"}

	switch runtime.GOOS {
	case "darwin":
		dev := opts.device
		if dev == "" {
			dev = getenvDefault("DESKBRIDGE_SCREEN_DEVICE", "Capture screen 0")
		}
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "avfoundation",
			"-framerate", strconv.Itoa(fps),
			"-capture_cursor", "1",
			"-i", dev,
		}
		return append(args, tail...), nil
	case "linux":
		dev := opts.device
		if dev == "" {
			dev = getenvDefault("DISPLAY", ":0.0")
		}
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "x11grab",
			"-framerate", strconv.Itoa(fps),
			"-i", dev,
		}
		return append(args, tail...), nil
	default:
		return nil, fmt.Errorf("screen capture is not supported on %s", runtime.GOOS)
	}
}

// streamMJPEG runs ffmpeg with the given args, splits its MJPEG stdout into
// individual JPEGs and writes each as a framed payload to w. The ffmpeg process
// is bound to ctx and killed when ctx is cancelled or w errors (e.g. the peer
// closed the stream). This is the shared core used by both live capture
// (pushScreen) and the headless loopback test (which feeds it a testsrc source).
func streamMJPEG(ctx context.Context, w io.Writer, ffmpegArgs []string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg was not found in PATH; install ffmpeg to use remote desktop capture")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// Ensure the encoder dies with us even if split returns early.
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	splitErr := splitMJPEG(stdout, func(frame []byte) error {
		return writeFrame(w, frame)
	})
	return splitErr
}

// pushScreen captures the local screen and pushes framed MJPEG to w. Called by
// the capturer side of the relay when it accepts a service-3 stream.
func pushScreen(ctx context.Context, w io.Writer, opts captureOptions) error {
	args, err := captureArgs(opts)
	if err != nil {
		return err
	}
	return streamMJPEG(ctx, w, args)
}

// readScreenFrames reads framed MJPEG from r and hands each JPEG to emit. The
// viewer side uses this to feed a decoder/display.
func readScreenFrames(r io.Reader, emit func([]byte) error) error {
	for {
		frame, err := readFrame(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := emit(frame); err != nil {
			return err
		}
	}
}

// ControlEvent is the absolute-coordinate input event serialized over service 4.
// Coordinates are normalized 0..1 against the captured frame so the capturer can
// map them onto its own display resolution regardless of the viewer's window
// size. This is the wire contract the Electron integration and the engine's
// input-injection layer will consume later.
type ControlEvent struct {
	Type   string  `json:"type"`             // "mouse-move" | "mouse-button" | "scroll" | "key"
	X      float64 `json:"x,omitempty"`      // normalized 0..1 (mouse)
	Y      float64 `json:"y,omitempty"`      // normalized 0..1 (mouse)
	Button int     `json:"button,omitempty"` // 0=left,1=middle,2=right
	Down   bool    `json:"down,omitempty"`   // press vs release (button/key)
	DX     float64 `json:"dx,omitempty"`     // scroll delta
	DY     float64 `json:"dy,omitempty"`     // scroll delta
	Key    string  `json:"key,omitempty"`    // logical key name (key events)
	Code   string  `json:"code,omitempty"`   // physical key code (key events)
}

// serveControlSink reads framed ControlEvents from the peer (capturer side of a
// service-4 stream) and dispatches them to the input injector.
//
// STUB: injection is logged, not performed. Real injection wires into the
// engine's platform input layer (see integration notes in the deliverable). We
// deliberately do NOT reach into engine/src here — that is another agent's file.
func serveControlSink(r io.Reader) error {
	for {
		frame, err := readFrame(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var ev ControlEvent
		if err := json.Unmarshal(frame, &ev); err != nil {
			// Skip malformed events rather than tearing down the channel.
			continue
		}
		injectControl(ev)
	}
}

// injectControl is the seam where an absolute input event becomes a real OS
// input injection. Phase 0 stub: log only.
func injectControl(ev ControlEvent) {
	fmt.Fprintf(os.Stderr, "[control] %+v\n", ev)
}

// serveViewerBridge runs a tiny local HTTP server that serves the standalone
// canvas viewer and bridges it to the raw framed loopback ports, because a
// browser cannot open raw TCP. It:
//   - serves the viewer HTML at "/",
//   - accepts a WebSocket at "/ws", dials the local screen port, and forwards
//     each framed JPEG to the browser as a binary WS message,
//   - forwards control events (JSON text WS messages) to the local control port
//     using the same [u32][payload] framing.
//
// This is a SPIKE helper for standalone testing; the Electron app will instead
// read the loopback ports directly from the main process.
func serveViewerBridge(ctx context.Context, httpPort, screenPort, controlPort int, html string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, html)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		wsCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		screenConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", screenPort))
		if err != nil {
			_ = c.Close(websocket.StatusInternalError, "screen port unavailable")
			return
		}
		defer screenConn.Close()

		// Frames peer -> browser.
		go func() {
			defer cancel()
			_ = readScreenFrames(screenConn, func(frame []byte) error {
				return c.Write(wsCtx, websocket.MessageBinary, frame)
			})
		}()

		// Control events browser -> peer (lazy dial; tolerate its absence).
		var controlConn net.Conn
		defer func() {
			if controlConn != nil {
				controlConn.Close()
			}
		}()
		for {
			typ, data, err := c.Read(wsCtx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText && typ != websocket.MessageBinary {
				continue
			}
			if controlConn == nil {
				controlConn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort))
				if err != nil {
					controlConn = nil
					continue
				}
			}
			if err := writeFrame(controlConn, data); err != nil {
				controlConn.Close()
				controlConn = nil
			}
		}
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", httpPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	fmt.Printf("Remote-desktop viewer bridge on http://127.0.0.1:%d (open in a browser)\n", httpPort)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// cmdScreenView starts the standalone browser viewer bridge. Run this on the
// viewer machine after `deskbridge connect` is up; it serves the canvas viewer
// and bridges it to the loopback screen/control ports the relay exposes.
func (c *cli) cmdScreenView(args []string) error {
	fs := flag.NewFlagSet("screen-view", flag.ContinueOnError)
	httpPort := fs.Int("http-port", viewerBridgePort, "local HTTP/WebSocket bridge port")
	screenPort := fs.Int("screen-port", screenViewPort, "relay loopback screen port")
	controlPort := fs.Int("control-port", controlInputPort, "relay loopback control port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveViewerBridge(ctx, *httpPort, *screenPort, *controlPort, remoteViewerHTML)
}

// cmdScreenTest is a self-contained loopback proof of the video pipe. It does
// NOT touch the real relay or peer: it runs ffmpeg against a synthetic source
// (or the real screen with --live), pushes framed MJPEG through a local TCP
// loopback, deframes on the other end, and writes decoded JPEGs to disk after
// validating each one decodes as an image.
func (c *cli) cmdScreenTest(args []string) error {
	fs := flag.NewFlagSet("screen-test", flag.ContinueOnError)
	out := fs.String("out", filepath.Join(os.TempDir(), "deskbridge-screen"), "directory for decoded frames")
	frames := fs.Int("frames", 5, "number of frames to capture")
	live := fs.Bool("live", false, "capture the real screen instead of a synthetic test source")
	width := fs.Int("width", 640, "downscale width")
	fps := fs.Int("fps", 5, "capture frame rate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}

	// Build the ffmpeg source args: synthetic testsrc (headless, no permissions)
	// or the real capture path shared with pushScreen.
	var ffArgs []string
	if *live {
		var err error
		ffArgs, err = captureArgs(captureOptions{width: *width, fps: *fps, quality: 7})
		if err != nil {
			return err
		}
	} else {
		ffArgs = []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi",
			"-i", fmt.Sprintf("testsrc=size=%dx480:rate=%d", *width, *fps),
			"-c:v", "mjpeg", "-q:v", "7", "-f", "mjpeg", "-",
		}
	}

	// Local TCP loopback: producer dials, consumer listens.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Producer: capture -> frame -> loopback.
	prodErr := make(chan error, 1)
	go func() {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			prodErr <- err
			return
		}
		defer conn.Close()
		prodErr <- streamMJPEG(ctx, conn, ffArgs)
	}()

	// Consumer: deframe -> validate -> save.
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	saved := 0
	readErr := readScreenFrames(conn, func(frame []byte) error {
		if _, decErr := jpeg.Decode(bytes.NewReader(frame)); decErr != nil {
			return fmt.Errorf("frame %d failed to decode as JPEG: %w", saved, decErr)
		}
		path := filepath.Join(*out, fmt.Sprintf("frame_%03d.jpg", saved))
		if err := os.WriteFile(path, frame, 0644); err != nil {
			return err
		}
		fmt.Printf("frame %d: %d bytes -> %s (valid JPEG)\n", saved, len(frame), path)
		saved++
		if saved >= *frames {
			return io.EOF // stop after the requested count
		}
		return nil
	})
	cancel()
	<-prodErr

	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	if saved == 0 {
		return errors.New("no frames were captured (is ffmpeg working?)")
	}
	fmt.Printf("OK: captured, framed, deframed and validated %d JPEG frame(s) in %s\n", saved, *out)
	return nil
}

// remoteViewerHTML is the standalone canvas viewer served by the bridge. It is
// kept byte-for-byte in sync with docs/remote-viewer.html (the canonical copy);
// edit that file and mirror changes here.
const remoteViewerHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DeskBridge Remote Viewer (spike)</title>
<style>
  html,body{margin:0;height:100%;background:#0c0f12;color:#e8eef2;font:13px system-ui,-apple-system,sans-serif}
  header{display:flex;gap:14px;align-items:center;padding:8px 14px;background:#141a20;border-bottom:1px solid #232c34}
  header b{font-size:14px}
  #stat{color:#8fa3b0}
  #wrap{position:absolute;inset:44px 0 0 0;display:grid;place-items:center;overflow:hidden}
  canvas{max-width:100%;max-height:100%;background:#000;cursor:crosshair;outline:none}
  .dot{width:8px;height:8px;border-radius:50%;background:#e0574a;display:inline-block}
  .dot.on{background:#2ecc71}
  label{color:#8fa3b0}
</style>
</head>
<body>
<header>
  <b>DeskBridge Remote Viewer</b>
  <span class="dot" id="led"></span>
  <span id="stat">connecting…</span>
  <label><input type="checkbox" id="ctl" checked> send input</label>
  <span id="fps"></span>
</header>
<div id="wrap"><canvas id="cv" tabindex="0" width="1280" height="720"></canvas></div>
<script>
// Standalone SPIKE viewer. Connects to the local bridge (deskbridge screen-view)
// over WebSocket. Each binary message is one JPEG frame; canvas mouse/keyboard
// events are normalized to 0..1 and sent back as JSON control events (service 4).
const cv = document.getElementById('cv');
const ctx = cv.getContext('2d');
const stat = document.getElementById('stat');
const led = document.getElementById('led');
const fpsEl = document.getElementById('fps');
const ctl = document.getElementById('ctl');

const wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws';
let ws, frames = 0, last = performance.now();

function connect() {
  ws = new WebSocket(wsURL);
  ws.binaryType = 'arraybuffer';
  ws.onopen = () => { led.classList.add('on'); stat.textContent = 'connected'; };
  ws.onclose = () => { led.classList.remove('on'); stat.textContent = 'disconnected — retrying'; setTimeout(connect, 1500); };
  ws.onerror = () => { stat.textContent = 'error'; };
  ws.onmessage = async (ev) => {
    const bmp = await createImageBitmap(new Blob([ev.data], {type: 'image/jpeg'}));
    if (cv.width !== bmp.width || cv.height !== bmp.height) { cv.width = bmp.width; cv.height = bmp.height; }
    ctx.drawImage(bmp, 0, 0);
    bmp.close();
    frames++;
    const now = performance.now();
    if (now - last >= 1000) { fpsEl.textContent = frames + ' fps'; frames = 0; last = now; }
  };
}
connect();

function send(ev) {
  if (!ctl.checked || !ws || ws.readyState !== 1) return;
  ws.send(JSON.stringify(ev));
}
function norm(e) {
  const r = cv.getBoundingClientRect();
  return { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
           y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
}
cv.addEventListener('mousemove', e => { const p = norm(e); send({type:'mouse-move', x:p.x, y:p.y}); });
cv.addEventListener('mousedown', e => { const p = norm(e); send({type:'mouse-button', x:p.x, y:p.y, button:e.button, down:true}); e.preventDefault(); });
cv.addEventListener('mouseup',   e => { const p = norm(e); send({type:'mouse-button', x:p.x, y:p.y, button:e.button, down:false}); e.preventDefault(); });
cv.addEventListener('contextmenu', e => e.preventDefault());
cv.addEventListener('wheel', e => { send({type:'scroll', dx:e.deltaX, dy:e.deltaY}); e.preventDefault(); }, {passive:false});
cv.addEventListener('keydown', e => { send({type:'key', key:e.key, code:e.code, down:true}); e.preventDefault(); });
cv.addEventListener('keyup',   e => { send({type:'key', key:e.key, code:e.code, down:false}); e.preventDefault(); });
cv.addEventListener('click', () => cv.focus());
</script>
</body>
</html>
`
