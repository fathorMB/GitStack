'use strict';
const http = require('node:http');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const { verifyIdentity } = require('./identity.cjs');
const exec = promisify(execFile);
const apiUrl = process.env.GITSTACK_API_URL?.replace(/\/+$/, '');
const repo = process.env.PILOT_REPO;
const marker = 'retail-pilot-381';

async function database(write) {
  const sql = write
    ? "CREATE TABLE IF NOT EXISTS pilot (value text PRIMARY KEY); INSERT INTO pilot VALUES ('retail-pilot-381') ON CONFLICT DO NOTHING; SELECT value FROM pilot WHERE value='retail-pilot-381';"
    : "SELECT value FROM pilot WHERE value='retail-pilot-381';";
  const { stdout } = await exec('psql', ['-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-h', 'postgres', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-c', sql], { timeout: 15000 });
  if (!stdout.split(/\r?\n/).includes(marker)) throw new Error('database value missing');
  return marker;
}

async function checked(url, options = {}) {
  const r = await fetch(url, { ...options, signal: AbortSignal.timeout(15000) });
  if (!r.ok) throw new Error(`upstream HTTP ${r.status}`);
  return r.json();
}

async function identity(req) {
  const { keys } = await checked(new URL('/.well-known/gitstack-app-keys', apiUrl));
  return verifyIdentity(req, keys);
}

async function readBody(req) {
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > 4 * 1024 * 1024) throw new Error('body too large');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

async function report(req, user) {
  const input = await readBody(req);
  if (typeof input.screenshot !== 'string' || !input.screenshot.startsWith('data:image/png;base64,')) throw new Error('PNG required');
  const png = Buffer.from(input.screenshot.slice('data:image/png;base64,'.length), 'base64');
  if (!png.subarray(0, 8).equals(Buffer.from('89504e470d0a1a0a', 'hex'))) throw new Error('invalid PNG');
  const auth = { Authorization: `Bearer ${process.env.BOT_TOKEN}` };
  const form = new FormData();
  form.append('file', new Blob([png], { type: 'image/png' }), 'screenshot.png');
  const attachment = await checked(`${apiUrl}/repos/${repo}/issue-attachments`, { method: 'POST', headers: auth, body: form });
  return checked(`${apiUrl}/repos/${repo}/issues`, { method: 'POST', headers: { ...auth, 'Content-Type': 'application/json' }, body: JSON.stringify({ title: 'Segnalazione pilota', body: `Tester: ${user}`, attachmentIds: [attachment.id] }) });
}

const page = `<!doctype html><html><body><p id="data"></p><input type="file" id="screenshot" accept="image/png"><button id="send">Segnala un problema</button><p id="result"></p><script>
fetch('/data').then(r=>r.text()).then(t=>document.getElementById('data').textContent=t);
document.getElementById('send').onclick=()=>{const f=document.getElementById('screenshot').files[0];if(!f)return;const r=new FileReader();r.onload=async()=>{const response=await fetch('/report',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({screenshot:r.result})});document.getElementById('result').textContent=await response.text()};r.readAsDataURL(f)};
</script></body></html>`;

http.createServer(async (req, res) => {
  try {
    const path = req.url.split('?')[0];
    if (process.env.PILOT_ROLE === 'api') {
      if (req.method !== 'GET' || !['/data', '/write'].includes(path)) { res.writeHead(404).end(); return; }
      const value = await database(path === '/write');
      res.writeHead(200, { 'content-type': 'text/plain' }).end(value);
      return;
    }
    if (process.env.PILOT_ROLE !== 'sito' || !apiUrl || !repo || !process.env.BOT_TOKEN) throw new Error('missing configuration');
    const user = await identity(req);
    if (req.method === 'GET' && path === '/') { res.writeHead(200, { 'content-type': 'text/html' }).end(page); return; }
    if (req.method === 'GET' && path === '/data') {
      const r = await fetch('http://api:8080/data', { signal: AbortSignal.timeout(15000) });
      if (!r.ok) throw new Error('API unavailable');
      res.writeHead(200, { 'content-type': 'text/plain' }).end(await r.text()); return;
    }
    if (req.method === 'POST' && path === '/report') { const issue = await report(req, user); res.writeHead(201, { 'content-type': 'application/json' }).end(JSON.stringify(issue)); return; }
    res.writeHead(404).end();
  } catch (err) { console.error(err.message); if (!res.headersSent) res.writeHead(500).end('pilot request failed'); else res.end(); }
}).listen(8080, '0.0.0.0');
