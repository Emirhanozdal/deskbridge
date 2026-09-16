const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { resolveEngine } = require('../engine.cjs');

test('bundled engine discovery and explicit override', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'deskbridge-engine-'));
  try {
    assert.equal(resolveEngine(dir, undefined, 'linux'), undefined);
    const bundled = path.join(dir, 'DeskBridge Input.app', 'Contents', 'MacOS', 'deskbridge-input');
    fs.mkdirSync(path.dirname(bundled), { recursive: true });
    fs.writeFileSync(bundled, '', { mode: 0o755 });
    assert.equal(resolveEngine(dir, undefined, 'linux'), bundled);
    const override = path.join(dir, 'override');
    fs.writeFileSync(override, '', { mode: 0o755 });
    assert.equal(resolveEngine(dir, override, 'linux'), override);
    // Executable-bit discovery is POSIX-only: on Windows fs.accessSync(X_OK)
    // behaves like F_OK, so a non-exec override still resolves. Skip that leg
    // off-POSIX (the engine only ever runs this discovery on mac/linux anyway).
    if (process.platform !== 'win32') {
      fs.chmodSync(override, 0o644);
      assert.equal(resolveEngine(dir, override, 'linux'), bundled);
    }
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
