import test from 'node:test';
import assert from 'node:assert/strict';
import { authorizedRoom, roomFromAuth } from './worker.mjs';

test('valid authentication hashes select isolated rooms', () => {
  const hash = 'a'.repeat(64);
  assert.equal(roomFromAuth('Bearer ' + hash), hash);
});

test('invalid authorization never creates a room', () => {
  for (const header of [null, '', 'Bearer secret', 'Basic ' + 'a'.repeat(64), 'Bearer ' + 'A'.repeat(64)]) {
    assert.equal(roomFromAuth(header), '');
  }
});

test('owner relay accepts only its configured pair', () => {
  const own = 'a'.repeat(64);
  assert.equal(authorizedRoom('Bearer ' + own, { AUTH_HASH: own }), own);
  assert.equal(authorizedRoom('Bearer ' + 'b'.repeat(64), { AUTH_HASH: own }), '');
  assert.equal(authorizedRoom('Bearer ' + own, {}), '');
});

test('self-hosted relay isolates any valid private room', () => {
  const room = 'b'.repeat(64);
  assert.equal(authorizedRoom('Bearer ' + room, { PUBLIC_ROOMS: 'true' }), room);
});
