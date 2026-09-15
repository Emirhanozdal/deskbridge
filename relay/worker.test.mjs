import test from 'node:test';
import assert from 'node:assert/strict';
import { roomFromAuth } from './worker.mjs';

test('valid authentication hashes select isolated rooms', () => {
  const hash = 'a'.repeat(64);
  assert.equal(roomFromAuth('Bearer ' + hash), hash);
});

test('invalid authorization never creates a room', () => {
  for (const header of [null, '', 'Bearer secret', 'Basic ' + 'a'.repeat(64), 'Bearer ' + 'A'.repeat(64)]) {
    assert.equal(roomFromAuth(header), '');
  }
});
