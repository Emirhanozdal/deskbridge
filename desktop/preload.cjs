const { contextBridge, ipcRenderer, webUtils } = require('electron');
contextBridge.exposeInMainWorld('deskbridge', {
  state: () => ipcRenderer.invoke('state'),
  chooseFiles: () => ipcRenderer.invoke('choose-files'),
  sendDrop: files => ipcRenderer.invoke('send-files', files.map(f => webUtils.getPathForFile(f))),
  configure: settings => ipcRenderer.invoke('configure', settings),
  applyLayout: layout => ipcRenderer.invoke('apply-layout', layout),
  connect: () => ipcRenderer.invoke('connect'),
  pair: input => ipcRenderer.invoke('pair', input),
  generatePairing: () => ipcRenderer.invoke('generate-pairing'),
  copyText: value => ipcRenderer.invoke('copy-text', value),
  showFile: id => ipcRenderer.invoke('show-file', id),
  copyFiles: id => ipcRenderer.invoke('copy-files', id),
  folder: () => ipcRenderer.invoke('folder'),
  startDrag: id => ipcRenderer.send('start-drag', id),
  changed: fn => { ipcRenderer.on('changed', (_,state) => fn(state)); },
});
