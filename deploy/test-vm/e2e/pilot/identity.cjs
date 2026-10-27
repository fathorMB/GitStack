'use strict';

// Verify H6 headers before attributing a report to its sender.
const { createPublicKey, verify } = require('node:crypto');

function verifyIdentity(req, keys, now = Date.now()) {
  const h = req.headers;
  const timestamp = h['x-gitstack-auth-time'];
  const user = h['x-gitstack-user'];
  const signature = h['x-gitstack-signature'];
  const key = keys.find(k => k.kid === h['x-gitstack-key-id'] && k.alg === 'Ed25519');
  if (!key || !user || !signature || !/^\d+$/.test(timestamp || '') ||
      Math.abs(now / 1000 - Number(timestamp)) > 60) throw new Error('invalid identity');
  const raw = Buffer.from(key.publicKey, 'base64');
  if (raw.length !== 32 || !/^[A-Za-z0-9_-]+$/.test(signature)) throw new Error('invalid identity');
  const publicKey = createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), raw]), format: 'der', type: 'spki' });
  const message = ['gitstack-app-identity-v1', req.method.toUpperCase(), h.host.toLowerCase(),
    req.url.split('?')[0], timestamp, user, h['x-gitstack-email'] || '', h['x-gitstack-teams'] || ''].join('\n');
  if (!verify(null, Buffer.from(message), publicKey, Buffer.from(signature, 'base64url'))) throw new Error('invalid identity');
  return user;
}

module.exports = { verifyIdentity };
