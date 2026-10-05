// Test del gate di sicurezza (GIT-114): node --test scripts/security/
// Coprono il criterio «su un tag di prova una vulnerabilità alta non presente
// nelle eccezioni fa fallire il job»: lo fa il codice di uscita del gate in
// modalità --release, qui provato con report finti.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { evaluate, loadExceptions, parseEslint, parseGosec, parseGovulncheck, parsePnpmAudit, parseTrivy } from './lib.mjs';
import { main } from './gate.mjs';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gate-'));
test.after(() => fs.rmSync(dir, { recursive: true, force: true }));

function write(name, content) {
  const p = path.join(dir, name);
  fs.writeFileSync(p, typeof content === 'string' ? content : JSON.stringify(content));
  return p;
}

const trivyHigh = (id = 'CVE-2099-0001', severity = 'HIGH') => ({
  ArtifactName: 'gitstack-core:test',
  Results: [
    {
      Target: 'alpine',
      Vulnerabilities: [
        { VulnerabilityID: id, Severity: severity, PkgName: 'openssl', InstalledVersion: '1.0', FixedVersion: '1.1', Title: 'prova' },
      ],
    },
  ],
});

const exceptions = (list) => write('ex.json', { exceptions: list });
const valid = (over = {}) => ({ id: 'CVE-2099-0001', tool: 'trivy', reason: 'falso positivo di prova', expires: '2999-01-01', ...over });

// main() stampa le annotazioni: nei test non serve vederle.
function run(args) {
  const log = console.log;
  console.log = () => {};
  try {
    return main(args);
  } finally {
    console.log = log;
  }
}

test('release: un HIGH senza eccezione fa fallire il gate', () => {
  const r = write('t1.json', trivyHigh());
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([]), r]), 1);
});

test('release: un CRITICAL fa fallire, un MEDIUM no', () => {
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([]), write('t2.json', trivyHigh('CVE-2099-0002', 'CRITICAL'))]), 1);
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([]), write('t3.json', trivyHigh('CVE-2099-0003', 'MEDIUM'))]), 0);
});

test('segnalazione: lo stesso HIGH non blocca', () => {
  const r = write('t4.json', trivyHigh());
  assert.equal(run(['trivy', '--exceptions', exceptions([]), r]), 0);
});

test('release: un HIGH con eccezione valida passa', () => {
  const r = write('t5.json', trivyHigh());
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([valid()]), r]), 0);
});

test('release: un\'eccezione scaduta non vale più', () => {
  const r = write('t6.json', trivyHigh());
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([valid({ expires: '2020-01-01' })]), r]), 1);
});

test('release: l\'eccezione vale solo per lo strumento e l\'id indicati', () => {
  const r = write('t7.json', trivyHigh());
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([valid({ tool: 'gosec' })]), r]), 1);
  assert.equal(run(['trivy', '--release', '--exceptions', exceptions([valid({ id: 'CVE-2099-9999' })]), r]), 1);
});

test('un file di eccezioni senza motivo o scadenza non è valido, anche in segnalazione', () => {
  const r = write('t8.json', trivyHigh());
  assert.equal(run(['trivy', '--exceptions', exceptions([valid({ reason: '' })]), r]), 1);
  assert.equal(run(['trivy', '--exceptions', exceptions([valid({ expires: 'domani' })]), r]), 1);
  const { problems } = loadExceptions(exceptions([{ id: 'x', tool: 'trivy' }]));
  assert.equal(problems.length, 2);
});

test('il file delle eccezioni del repo è valido', () => {
  const file = path.join(path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')), '..', '..', '.github', 'security-exceptions.json');
  assert.deepEqual(loadExceptions(file).problems, []);
});

test('un report mancante o illeggibile fa fallire anche in segnalazione', () => {
  assert.equal(run(['trivy', '--exceptions', exceptions([])]), 1);
  assert.equal(run(['trivy', '--exceptions', exceptions([]), write('rotto.json', 'non json')]), 1);
});

test('eccezione con path: copre solo quel percorso', () => {
  const f = [
    { tool: 'gosec', id: 'G304', severity: 'HIGH', title: 't', location: 'a/x.go:1' },
    { tool: 'gosec', id: 'G304', severity: 'HIGH', title: 't', location: 'b/y.go:2' },
  ];
  const { entries } = loadExceptions(exceptions([valid({ tool: 'gosec', id: 'G304', path: 'a/x.go' })]));
  const r = evaluate(f, entries);
  assert.equal(r.excepted.length, 1);
  assert.equal(r.blocking.length, 1);
});

test('parser govulncheck: solo le vulnerabilità chiamate sono HIGH', () => {
  const text = [
    { osv: { id: 'GO-1', summary: 'una' } },
    { finding: { osv: 'GO-1', fixed_version: 'v1.2', trace: [{ module: 'stdlib', version: 'v1.0', function: 'F' }] } },
    { osv: { id: 'GO-2', summary: 'due' } },
    { finding: { osv: 'GO-2', trace: [{ module: 'x/y', version: 'v1' }] } },
  ]
    .map((o) => JSON.stringify(o))
    .join('\n');
  const f = parseGovulncheck(text, 'core');
  assert.deepEqual(
    f.map((x) => [x.id, x.severity]),
    [['GO-1', 'HIGH'], ['GO-2', 'MEDIUM']],
  );
});

test('parser gosec, eslint, pnpm audit', () => {
  assert.equal(parseGosec(JSON.stringify({ Issues: [{ rule_id: 'G101', severity: 'HIGH', details: 'd', file: 'f.go', line: '3' }] }))[0].severity, 'HIGH');
  const es = parseEslint(JSON.stringify([{ filePath: 'a.ts', messages: [{ ruleId: 'r', severity: 2, message: 'm', line: 1 }, { ruleId: null, severity: 2, message: 'parse', line: 1 }] }]));
  assert.equal(es.length, 1);
  assert.equal(es[0].severity, 'HIGH');
  const pa = parsePnpmAudit(JSON.stringify({ advisories: { 1: { id: 1, github_advisory_id: 'GHSA-x', severity: 'moderate', title: 't', module_name: 'm' } } }));
  assert.deepEqual([pa[0].id, pa[0].severity], ['GHSA-x', 'MEDIUM']);
  assert.equal(parseTrivy(JSON.stringify(trivyHigh()))[0].severity, 'HIGH');
});
