const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const plist = require('../desktop/node_modules/plist');
const { inputSettings } = require('../desktop/input-settings.cjs');

if (process.platform !== 'darwin') throw Error('This installer supports macOS only');
const root = path.resolve(__dirname, '..');
const source = path.join(root, 'work', 'dist', 'DeskBridge-darwin-arm64', 'DeskBridge.app');
const installed = '/Applications/DeskBridge.app';
if (!fs.existsSync(source)) throw Error('Run node scripts/package-macos.cjs first');
execFileSync('/usr/bin/ditto', [source, installed], { stdio: 'inherit' });
const logs = path.join(os.homedir(), 'Library', 'Logs', 'DeskBridge');
const agents = path.join(os.homedir(), 'Library', 'LaunchAgents');
fs.mkdirSync(logs, { recursive: true });
fs.mkdirSync(agents, { recursive: true });
const inputConfigDir = path.join(os.homedir(), 'Library', 'Application Support', 'deskbridge');
const inputConfig = path.join(inputConfigDir, 'input.ini');
const layout = path.join(os.homedir(), 'Library', 'Deskflow', 'deskflow-server.conf');
if (!fs.existsSync(layout)) throw Error('DeskBridge screen layout is missing: ' + layout);
fs.mkdirSync(inputConfigDir, { recursive: true, mode: 0o700 });
fs.writeFileSync(inputConfig, inputSettings('server', os.hostname(), layout), { mode: 0o600 });
const services = [
  {
    label: 'com.deskbridge.relay',
    arguments: [path.join(installed, 'Contents', 'Resources', 'deskbridge'), 'connect'],
    output: 'relay'
  },
  {
    label: 'com.deskbridge.input',
    arguments: [path.join(installed, 'Contents', 'Resources', 'DeskBridge Input.app', 'Contents', 'MacOS', 'deskbridge-input'), 'server', '--settings', inputConfig],
    output: 'input'
  }
];
for (const service of services) {
  const file = path.join(agents, service.label + '.plist');
  const previous = file + '.previous';
  if (fs.existsSync(file)) fs.copyFileSync(file, previous);
  fs.writeFileSync(file, plist.build({
    Label: service.label,
    ProgramArguments: service.arguments,
    RunAtLoad: true,
    KeepAlive: true,
    ThrottleInterval: 10,
    ProcessType: 'Background',
    StandardOutPath: path.join(logs, service.output + '.log'),
    StandardErrorPath: path.join(logs, service.output + '-error.log')
  }), { mode: 0o600 });
  try { execFileSync('/bin/launchctl', ['bootout', 'gui/' + process.getuid(), file], { stdio: 'ignore' }); } catch {}
  execFileSync('/bin/launchctl', ['bootstrap', 'gui/' + process.getuid(), file], { stdio: 'inherit' });
  if (service.label === 'com.deskbridge.input') {
    execFileSync('/bin/sleep', ['2']);
    try {
      execFileSync('/usr/sbin/lsof', ['-t', '-iTCP:24800', '-sTCP:LISTEN'], { stdio: 'ignore' });
    } catch (failure) {
      if (fs.existsSync(previous)) {
        fs.copyFileSync(previous, file);
        try { execFileSync('/bin/launchctl', ['bootout', 'gui/' + process.getuid(), file], { stdio: 'ignore' }); } catch {}
        execFileSync('/bin/launchctl', ['bootstrap', 'gui/' + process.getuid(), file], { stdio: 'inherit' });
      }
      throw Error('DeskBridge Input needs macOS Accessibility permission; the previous input service was restored');
    }
  }
}
console.log(installed);
