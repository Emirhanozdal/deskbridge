const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

if (process.platform !== 'darwin') throw Error('This packager creates the macOS build only');
const root = path.resolve(__dirname, '..');
const relay = path.join(root, 'build', 'deskbridge');
const input = path.join(root, 'work', 'embedded-input', 'DeskBridge Input.app');
for (const required of [relay, input]) {
  if (!fs.existsSync(required)) throw Error('Missing build artifact: ' + required);
}
const packager = path.join(root, 'desktop', 'node_modules', '@electron', 'packager', 'bin', 'electron-packager.mjs');
function findElectronZip(directory) {
  if (!fs.existsSync(directory)) return;
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      const found = findElectronZip(file);
      if (found) return found;
    } else if (entry.name === 'electron-v44.3.0-darwin-arm64.zip') return file;
  }
}
const electronZip = findElectronZip(path.join(os.homedir(), 'Library', 'Caches', 'electron'));
if (!electronZip) throw Error('Electron 44.3.0 arm64 cache is missing; run npm install in desktop');
execFileSync(process.execPath, [
  packager,
  path.join(root, 'desktop'),
  'DeskBridge',
  '--platform=darwin',
  '--arch=arm64',
  '--out=' + path.join(root, 'work', 'dist'),
  '--overwrite',
  '--asar',
  '--app-bundle-id=com.deskbridge.app',
  '--icon', path.join(root, 'desktop', 'icon.icns'),
  '--app-version=0.3.7',
  '--build-version=0.3.7',
  '--electron-zip-dir=' + path.dirname(electronZip),
  '--extra-resource=' + relay,
  '--extra-resource=' + input
], { stdio: 'inherit' });
const app = path.join(root, 'work', 'dist', 'DeskBridge-darwin-arm64', 'DeskBridge.app');
const packagedInput = path.join(app, 'Contents', 'Resources', 'DeskBridge Input.app');
function repairCopiedLinks(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    const stat = fs.lstatSync(file);
    if (stat.isSymbolicLink()) {
      const target = fs.readlinkSync(file);
      if (path.isAbsolute(target) && target.startsWith(input + path.sep)) {
        const packagedTarget = path.join(packagedInput, path.relative(input, target));
        fs.unlinkSync(file);
        fs.symlinkSync(path.relative(path.dirname(file), packagedTarget), file);
      }
    } else if (stat.isDirectory()) repairCopiedLinks(file);
  }
}
repairCopiedLinks(packagedInput);
execFileSync('codesign', ['--force', '--deep', '--sign', '-', app], { stdio: 'inherit' });
execFileSync('codesign', ['--verify', '--deep', '--strict', app], { stdio: 'inherit' });
console.log(app);
