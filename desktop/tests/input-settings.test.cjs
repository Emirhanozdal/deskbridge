const { test } = require('node:test');
const assert = require('node:assert/strict');
const { inputSettings } = require('../input-settings.cjs');

test('server settings use the encrypted DeskBridge transport', () => {
  const settings = inputSettings('server', 'mac', '/tmp/layout.conf');
  assert.match(settings, /computerName="mac"/);
  assert.match(settings, /tlsEnabled=false/);
  assert.match(settings, /externalConfigFile="\/tmp\/layout.conf"/);
});

test('Linux client connects only to the local DeskBridge proxy', () => {
  const settings = inputSettings('client', 'mint');
  assert.match(settings, /computerName="mint"/);
  assert.match(settings, /remoteHost="127\.0\.0\.1:24801"/);
  assert.doesNotMatch(settings, /externalConfig/);
});

test('settings reject line injection', () => {
  assert.throws(() => inputSettings('client', 'mint\n[server]'));
});
