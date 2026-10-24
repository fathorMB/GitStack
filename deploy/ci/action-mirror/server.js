// Mirror finto delle action per la prova del job `chart` (GIT-317).
//
// Serve via HTTP "smart" (git-upload-pack) i repo bare sotto ROOT, es.
// ROOT/actions/checkout.git per http://<host>:<porta>/actions/checkout. Ogni richiesta
// finisce nel log con il prefisso "MIRROR-HIT", cosi' la prova puo' dimostrare che le
// action arrivano da qui e non da github.com. Solo lettura, nessuna dipendenza.
'use strict';
const http = require('http');
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');

const root = path.resolve(process.env.MIRROR_ROOT || '/tmp/mirror');
const port = Number(process.env.MIRROR_PORT || 8080);

function pktLine(s) {
  return (Buffer.byteLength(s) + 4).toString(16).padStart(4, '0') + s;
}

function repoDir(urlPath) {
  const m = /^\/([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+?)(?:\.git)?\/(info\/refs|git-upload-pack)$/.exec(urlPath);
  if (!m) return null;
  const dir = path.join(root, m[1], m[2] + '.git');
  return fs.existsSync(dir) ? { dir, op: m[3] } : null;
}

http.createServer((req, res) => {
  const u = new URL(req.url, 'http://x');
  console.log(`MIRROR-HIT ${req.method} ${u.pathname}${u.search}`);
  const hit = repoDir(u.pathname);
  if (!hit) {
    res.writeHead(404).end('not found\n');
    return;
  }
  if (hit.op === 'info/refs' && req.method === 'GET' && u.searchParams.get('service') === 'git-upload-pack') {
    res.writeHead(200, { 'Content-Type': 'application/x-git-upload-pack-advertisement', 'Cache-Control': 'no-cache' });
    res.write(pktLine('# service=git-upload-pack\n') + '0000');
    const p = spawn('git', ['upload-pack', '--stateless-rpc', '--advertise-refs', hit.dir]);
    p.stdout.pipe(res);
    p.stderr.pipe(process.stderr);
    return;
  }
  if (hit.op === 'git-upload-pack' && req.method === 'POST') {
    res.writeHead(200, { 'Content-Type': 'application/x-git-upload-pack-result', 'Cache-Control': 'no-cache' });
    const p = spawn('git', ['upload-pack', '--stateless-rpc', hit.dir]);
    // go-git puo' mandare il corpo compresso con gzip.
    const body = req.headers['content-encoding'] === 'gzip' ? req.pipe(require('zlib').createGunzip()) : req;
    body.pipe(p.stdin);
    p.stdout.pipe(res);
    p.stderr.pipe(process.stderr);
    return;
  }
  res.writeHead(405).end('method not allowed\n');
}).listen(port, () => console.log(`MIRROR-LISTEN ${port} ${root}`));
