// Main-process side of the in-app remote-desktop viewer. It opens a dedicated
// BrowserWindow (remote.html) and bridges it to the relay's loopback ports that
// screen.go/relay.go expose while `deskbridge connect` is running:
//
//   screen  127.0.0.1:47893  -> framed MJPEG in  ([u32 BE len][JPEG] frames)
//   control 127.0.0.1:47895  -> framed control events out ([u32 BE len][JSON])
//
// Frames arrive over a raw TCP socket (a browser can't read raw TCP, so the main
// process deframes and forwards each JPEG to the renderer via IPC). Control events
// travel the other way: the renderer emits normalized 0..1 events, the main
// process frames them and writes to the control port. This keeps the app's
// contextIsolation:true / sandbox:true / IPC-via-preload contract intact — the
// renderer never touches Node or the network directly.
//
// The direction is implicit: opening the viewer on THIS machine always shows and
// controls the PEER's screen (the peer answers screen requests by capturing its
// own display). The `label` is cosmetic, to remind the user which way they're
// looking.

const net = require('node:net');
const path = require('node:path');

const SCREEN_PORT = 47893;
const CONTROL_PORT = 47895;

// A viewer sends its desired capture settings as the FIRST framed payload on the
// screen stream (see streamNegotiation in screen.go). Zero fields => capturer
// defaults.
function framePayload(buf) {
  const header = Buffer.alloc(4);
  header.writeUInt32BE(buf.length, 0);
  return Buffer.concat([header, buf]);
}

// A stateful deframer for the [u32 BE len][payload] wire format. Feed it socket
// chunks; it calls onFrame(payload) once per complete frame.
function createDeframer(onFrame) {
  let acc = Buffer.alloc(0);
  const MAX = 16 * 1024 * 1024; // mirror screenMaxFrameLen
  return chunk => {
    acc = acc.length ? Buffer.concat([acc, chunk]) : chunk;
    for (;;) {
      if (acc.length < 4) return;
      const len = acc.readUInt32BE(0);
      if (len === 0 || len > MAX) {
        // Corrupt stream; drop everything so we resync on the next connection.
        acc = Buffer.alloc(0);
        return;
      }
      if (acc.length < 4 + len) return;
      const payload = acc.subarray(4, 4 + len);
      onFrame(Buffer.from(payload));
      acc = acc.subarray(4 + len);
    }
  };
}

function createRemote({ BrowserWindow, ipcMain, iconPath }) {
  let win = null;
  let screenSock = null;
  let controlSock = null;
  let opts = { width: 1280, fps: 12, quality: 7 };

  function status(text) {
    if (win && !win.isDestroyed()) win.webContents.send('remote-status', text);
  }

  function closeScreen() {
    if (screenSock) { screenSock.destroy(); screenSock = null; }
  }

  function openScreen() {
    closeScreen();
    const sock = net.connect(SCREEN_PORT, '127.0.0.1');
    screenSock = sock;
    const deframe = createDeframer(payload => {
      if (win && !win.isDestroyed()) win.webContents.send('remote-frame', payload);
    });
    sock.on('connect', () => {
      // Send the negotiation header first, then read frames.
      const nego = { width: opts.width | 0, fps: opts.fps | 0, quality: opts.quality | 0 };
      sock.write(framePayload(Buffer.from(JSON.stringify(nego))));
      status('connected');
    });
    sock.on('data', deframe);
    sock.on('error', () => status('screen error — is the peer connected?'));
    sock.on('close', () => {
      if (sock === screenSock) {
        screenSock = null;
        status('disconnected');
      }
    });
  }

  function ensureControl() {
    if (controlSock && !controlSock.destroyed) return controlSock;
    const sock = net.connect(CONTROL_PORT, '127.0.0.1');
    controlSock = sock;
    sock.on('error', () => { if (sock === controlSock) controlSock = null; });
    sock.on('close', () => { if (sock === controlSock) controlSock = null; });
    return sock;
  }

  function sendControl(ev) {
    const sock = ensureControl();
    try { sock.write(framePayload(Buffer.from(JSON.stringify(ev)))); } catch {}
  }

  const fromWin = event => win && event.sender === win.webContents;

  // Registered once; all guarded so only the remote window can drive them.
  ipcMain.on('remote-ready', event => { if (fromWin(event)) openScreen(); });
  ipcMain.on('remote-control', (event, ev) => { if (fromWin(event) && ev && typeof ev.type === 'string') sendControl(ev); });
  ipcMain.on('remote-negotiate', (event, next) => {
    if (!fromWin(event) || !next) return;
    if (Number.isFinite(next.width)) opts.width = Math.max(160, Math.min(3840, next.width | 0));
    if (Number.isFinite(next.fps)) opts.fps = Math.max(1, Math.min(60, next.fps | 0));
    if (Number.isFinite(next.quality)) opts.quality = Math.max(2, Math.min(31, next.quality | 0));
    openScreen(); // reconnect so ffmpeg picks up the new args
  });

  function open(initial = {}) {
    if (initial && typeof initial === 'object') {
      opts = {
        width: Number.isFinite(initial.width) ? initial.width | 0 : opts.width,
        fps: Number.isFinite(initial.fps) ? initial.fps | 0 : opts.fps,
        quality: Number.isFinite(initial.quality) ? initial.quality | 0 : opts.quality,
      };
    }
    if (win && !win.isDestroyed()) { win.show(); win.focus(); return; }
    win = new BrowserWindow({
      width: 1200, height: 760, minWidth: 640, minHeight: 420,
      title: 'DeskBridge · Uzak Ekran', backgroundColor: '#0c0f12',
      icon: iconPath,
      webPreferences: {
        preload: path.join(__dirname, 'remote-preload.cjs'),
        contextIsolation: true, nodeIntegration: false, sandbox: true,
      },
    });
    win.loadFile(path.join(__dirname, 'remote.html'));
    win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    win.webContents.on('will-navigate', e => e.preventDefault());
    win.on('closed', () => { win = null; closeScreen(); if (controlSock) { controlSock.destroy(); controlSock = null; } });
  }

  function destroy() {
    closeScreen();
    if (controlSock) { controlSock.destroy(); controlSock = null; }
    if (win && !win.isDestroyed()) win.destroy();
    win = null;
  }

  return { open, destroy };
}

module.exports = { createRemote };
