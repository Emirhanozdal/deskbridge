const { contextBridge, ipcRenderer } = require('electron');

// Bridge for the remote-desktop window. The renderer stays fully sandboxed: it
// receives JPEG frame buffers and status strings from the main process, and sends
// normalized control events / renegotiation requests back. No Node, no network.
contextBridge.exposeInMainWorld('remote', {
  ready: () => ipcRenderer.send('remote-ready'),
  onFrame: cb => ipcRenderer.on('remote-frame', (_, buf) => cb(buf)),
  onStatus: cb => ipcRenderer.on('remote-status', (_, text) => cb(text)),
  control: ev => ipcRenderer.send('remote-control', ev),
  negotiate: opts => ipcRenderer.send('remote-negotiate', opts),
});
