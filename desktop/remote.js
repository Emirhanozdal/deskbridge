// Renderer for the in-app remote-desktop window. Frames arrive from the main
// process as JPEG byte buffers (window.remote.onFrame); mouse/keyboard events are
// normalized to 0..1 against the canvas and sent back (window.remote.control).
// Ported from docs/remote-viewer.html but sandbox-safe: no WebSocket, no network.
const api = window.remote;
const cv = document.getElementById('cv');
const ctx = cv.getContext('2d');
const stat = document.getElementById('stat');
const led = document.getElementById('led');
const fpsEl = document.getElementById('fpsCounter');
const ctl = document.getElementById('ctl');
const qualitySel = document.getElementById('quality');
const fpsSel = document.getElementById('fps');
const widthSel = document.getElementById('width');

let frames = 0, last = performance.now();

api.onStatus(text => {
  stat.textContent = text;
  led.classList.toggle('on', text === 'connected');
});

api.onFrame(async buf => {
  // buf is a Uint8Array (IPC-serialized Buffer). Decode and paint.
  const bmp = await createImageBitmap(new Blob([buf], { type: 'image/jpeg' }));
  if (cv.width !== bmp.width || cv.height !== bmp.height) { cv.width = bmp.width; cv.height = bmp.height; }
  ctx.drawImage(bmp, 0, 0);
  bmp.close();
  frames++;
  const now = performance.now();
  if (now - last >= 1000) { fpsEl.textContent = frames + ' fps'; frames = 0; last = now; }
});

function send(ev) {
  if (!ctl.checked) return;
  api.control(ev);
}
function norm(e) {
  const r = cv.getBoundingClientRect();
  return {
    x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
    y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
  };
}
cv.addEventListener('mousemove', e => { const p = norm(e); send({ type: 'mouse-move', x: p.x, y: p.y }); });
cv.addEventListener('mousedown', e => { const p = norm(e); send({ type: 'mouse-button', x: p.x, y: p.y, button: e.button, down: true }); cv.focus(); e.preventDefault(); });
cv.addEventListener('mouseup', e => { const p = norm(e); send({ type: 'mouse-button', x: p.x, y: p.y, button: e.button, down: false }); e.preventDefault(); });
cv.addEventListener('contextmenu', e => e.preventDefault());
cv.addEventListener('wheel', e => { send({ type: 'scroll', dx: e.deltaX, dy: e.deltaY }); e.preventDefault(); }, { passive: false });
cv.addEventListener('keydown', e => { send({ type: 'key', key: e.key, code: e.code, down: true }); e.preventDefault(); });
cv.addEventListener('keyup', e => { send({ type: 'key', key: e.key, code: e.code, down: false }); e.preventDefault(); });

function renegotiate() {
  api.negotiate({
    width: parseInt(widthSel.value, 10) || 0,
    fps: parseInt(fpsSel.value, 10) || 12,
    quality: parseInt(qualitySel.value, 10) || 7,
  });
}
qualitySel.addEventListener('change', renegotiate);
fpsSel.addEventListener('change', renegotiate);
widthSel.addEventListener('change', renegotiate);

// Tell main we're ready to receive frames (it opens the sockets on this signal).
api.ready();
