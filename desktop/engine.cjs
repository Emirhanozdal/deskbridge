const path = require('node:path');
const fs = require('node:fs');

function resolveEngine(resources, override, platform = process.platform) {
  const candidates = [
    override,
    path.join(resources, 'DeskBridge Input.app', 'Contents', 'MacOS', 'deskbridge-input'),
    path.join(resources, 'deskbridge-input'),
    ...(platform === 'win32' ? [path.join(resources, 'deskbridge-input.exe')] : []),
    ...(platform === 'darwin' ? ['/Applications/Deskflow.app/Contents/MacOS/deskflow-core'] : [])
  ].filter(Boolean);
  return candidates.find(candidate => {
    try { fs.accessSync(candidate, fs.constants.X_OK); return fs.statSync(candidate).isFile(); }
    catch { return false; }
  });
}

module.exports = { resolveEngine };
