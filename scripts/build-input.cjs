const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');
const spec = require('../engine/source.json');
const source = path.resolve(process.argv[2] || path.join(root, spec.directory));
const build = path.join(root, 'work', 'embedded-input-build');
const output = path.join(root, 'work', 'embedded-input');
function run(command, args, options = {}) {
  return execFileSync(command, args, { stdio: 'inherit', ...options });
}
if (!fs.existsSync(path.join(source, 'CMakeLists.txt')) || !fs.existsSync(path.join(source, 'LICENSE'))) {
  throw Error('Embedded input source is missing from ' + source);
}
const options = ['-S', source, '-B', build, '-G', 'Ninja', '-DCMAKE_BUILD_TYPE=Release', '-D' + spec.buildOption];
let qt;
if (process.platform === 'darwin') {
  qt = process.env.QT_PREFIX || run('brew', ['--prefix', 'qtbase'], { encoding: 'utf8', stdio: 'pipe' }).trim();
  options.push('-DCMAKE_PREFIX_PATH=' + qt);
}
run('cmake', options);
run('cmake', ['--build', build, '--target', 'deskflow-core', '-j', '4']);
fs.mkdirSync(output, { recursive: true });
if (process.platform === 'darwin') {
  const bundle = path.join(output, 'DeskBridge Input.app');
  const contents = path.join(bundle, 'Contents');
  const macos = path.join(contents, 'MacOS');
  const resources = path.join(contents, 'Resources');
  fs.mkdirSync(macos, { recursive: true });
  fs.mkdirSync(resources, { recursive: true });
  fs.copyFileSync(path.join(build, 'bin', spec.binary), path.join(macos, spec.binary));
  const plist = require('../desktop/node_modules/plist');
  fs.writeFileSync(path.join(contents, 'Info.plist'), plist.build({
    CFBundleExecutable: spec.binary,
    CFBundleIdentifier: 'com.deskbridge.input',
    CFBundleName: 'DeskBridge Input',
    CFBundlePackageType: 'APPL',
    CFBundleVersion: '0.3.2',
    CFBundleShortVersionString: '0.3.2',
    LSUIElement: true,
    NSInputMonitoringUsageDescription: 'Share keyboard and mouse input with your paired computer.'
  }));
  for (const name of ['LICENSE', 'LICENSES', 'DESKBRIDGE.md']) {
    fs.cpSync(path.join(source, name), path.join(resources, name), { recursive: true });
  }
  fs.copyFileSync(path.join(root, 'engine', 'source.json'), path.join(resources, 'source.json'));
  const sourceCopy = path.join(resources, 'input-source');
  fs.rmSync(sourceCopy, { recursive: true, force: true });
  fs.cpSync(source, sourceCopy, {
    recursive: true,
    dereference: true,
    filter: file => !file.includes(path.sep + '.git' + path.sep) && !file.endsWith(path.sep + '.git')
  });
  run(path.join(qt, 'bin', 'macdeployqt'), [bundle, '-always-overwrite']);
  const dependencies = run('otool', ['-L', path.join(macos, spec.binary)], { encoding: 'utf8', stdio: 'pipe' });
  if (/\/opt\/homebrew\/|\/usr\/local\//.test(dependencies)) throw Error('Unbundled engine dependency remains');
  run('codesign', ['--force', '--deep', '--sign', '-', bundle]);
  run('codesign', ['--verify', '--deep', '--strict', bundle]);
  console.log(bundle);
} else {
  fs.copyFileSync(path.join(build, 'bin', spec.binary), path.join(output, spec.binary));
  console.log('Built ' + output + '; Linux runtime dependency packaging is still required.');
}
