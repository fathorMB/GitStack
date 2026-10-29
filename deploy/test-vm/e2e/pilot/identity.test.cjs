'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { generateKeyPairSync, sign } = require('node:crypto');
const { verifyIdentity } = require('./identity.cjs');

test('H6 accepts only a fresh signature for this method, host and path', () => {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const raw = publicKey.export({ type: 'spki', format: 'der' }).subarray(-32);
  const keys = [{ kid: 'one', alg: 'Ed25519', publicKey: raw.toString('base64') }];
  const req = { method: 'POST', url: '/report?x=1', headers: {
    host: 'Site.Example', 'x-gitstack-auth-time': '1000', 'x-gitstack-user': 'tester',
    'x-gitstack-email': '', 'x-gitstack-teams': '', 'x-gitstack-key-id': 'one'
  } };
  const message = 'gitstack-app-identity-v1\nPOST\nsite.example\n/report\n1000\ntester\n\n';
  req.headers['x-gitstack-signature'] = sign(null, Buffer.from(message), privateKey).toString('base64url');
  assert.equal(verifyIdentity(req, keys, 1000000), 'tester');
  assert.throws(() => verifyIdentity({ ...req, url: '/other' }, keys, 1000000));
  assert.throws(() => verifyIdentity({ ...req, method: 'GET' }, keys, 1000000));
  assert.throws(() => verifyIdentity(req, keys, 1061000));
  assert.throws(() => verifyIdentity({ ...req, headers: { ...req.headers, 'x-gitstack-user': 'attacker' } }, keys, 1000000));
  assert.throws(() => verifyIdentity(req, [], 1000000));
});
