// Controllo F19 (ASVS V1.1.2, GIT-239): ogni `uses:` dei workflow GitHub che
// non sia un workflow locale (./...) deve essere fissato a uno sha di commit
// di 40 esadecimali, non a un tag che si può spostare.
//
//   node --test scripts/check-actions-pinned.mjs
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import assert from 'node:assert/strict';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const dir = join(root, '.github', 'workflows');

// Restituisce i riferimenti non fissati di un testo YAML: [{line, ref}].
export function unpinned(text) {
  const out = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    const m = /^\s*(?:-\s+)?uses:\s*['"]?([^\s'"#]+)/.exec(raw);
    if (!m) return;
    const ref = m[1];
    if (ref.startsWith('./')) return;
    if (!/@[0-9a-f]{40}$/.test(ref)) out.push({ line: i + 1, ref });
  });
  return out;
}

test('every uses: is pinned to a commit', () => {
  const files = readdirSync(dir).filter((f) => /\.ya?ml$/.test(f));
  assert.ok(files.length > 0, 'nessun workflow trovato');
  const bad = [];
  for (const f of files) {
    for (const u of unpinned(readFileSync(join(dir, f), 'utf8'))) {
      bad.push(`${f}:${u.line} ${u.ref}`);
    }
  }
  assert.deepEqual(bad, [], `uses: non fissati a uno sha:\n${bad.join('\n')}`);
});

test('unpinned riconosce tag, branch e sha', () => {
  const sha = 'a'.repeat(40);
  const text = [
    `      - uses: actions/checkout@${sha} # v5`,
    '      - uses: actions/checkout@v5',
    '    uses: ./.github/workflows/security.yml',
    '      - uses: docker/login-action@main',
  ].join('\n');
  assert.deepEqual(
    unpinned(text).map((u) => u.ref),
    ['actions/checkout@v5', 'docker/login-action@main'],
  );
});
