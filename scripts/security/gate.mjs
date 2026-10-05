#!/usr/bin/env node
// Gate di sicurezza (GIT-114).
//
//   node scripts/security/gate.mjs validate
//   node scripts/security/gate.mjs <govulncheck|gosec|eslint|pnpm-audit|trivy>
//        --name "Titolo" [--release] [--exceptions file] report.json...
//
// Senza --release segnala (annotazioni + riepilogo nel run) ed esce 0.
// Con --release esce 1 se resta un finding CRITICAL/HIGH senza eccezione
// valida. --release si passa dal workflow sui tag v* e sul dispatch di prova.
// Un report mancante o illeggibile è un errore anche in segnalazione: uno
// strumento che non gira non deve sembrare "nessun finding".

import fs from 'node:fs';
import path from 'node:path';
import {
  annotations,
  evaluate,
  loadExceptions,
  markdown,
  parseEslint,
  parseGosec,
  parseGovulncheck,
  parsePnpmAudit,
  parseTrivy,
} from './lib.mjs';

const DEFAULT_EXCEPTIONS = '.github/security-exceptions.json';

function summary(text) {
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, text + '\n');
}

export function main(argv) {
  const [kind, ...rest] = argv;
  let name = kind;
  let release = false;
  let exFile = DEFAULT_EXCEPTIONS;
  const files = [];
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === '--name') name = rest[++i];
    else if (rest[i] === '--release') release = true;
    else if (rest[i] === '--exceptions') exFile = rest[++i];
    else files.push(rest[i]);
  }

  const { entries, problems } = loadExceptions(exFile);
  if (problems.length) {
    for (const p of problems) console.log(`::error title=Eccezioni di sicurezza non valide::${p}`);
    return 1;
  }
  for (const e of entries.filter((x) => x.expired)) {
    console.log(`::warning title=Eccezione scaduta::${e.tool} ${e.id} è scaduta il ${e.expires} e non vale più: rinnovala con un motivo o toglila.`);
  }
  if (kind === 'validate') {
    console.log(`${exFile}: ${entries.length} voci, ${entries.filter((e) => e.expired).length} scadute.`);
    return 0;
  }

  const parsers = {
    govulncheck: (t, f) => parseGovulncheck(t, path.basename(f).replace(/^vuln-|\.json$/g, '')),
    gosec: (t) => parseGosec(t),
    eslint: (t) => parseEslint(t),
    'pnpm-audit': (t) => parsePnpmAudit(t),
    trivy: (t, f) => parseTrivy(t, path.basename(f).replace(/^trivy-|\.json$/g, '')),
  };
  if (!parsers[kind]) {
    console.error(`strumento sconosciuto: ${kind}`);
    return 2;
  }
  if (files.length === 0) {
    console.log(`::error title=${name}::nessun report passato: lo strumento non ha prodotto output`);
    return 1;
  }

  const findings = [];
  for (const f of files) {
    try {
      findings.push(...parsers[kind](fs.readFileSync(f, 'utf8'), f));
    } catch (e) {
      console.log(`::error title=${name}::report ${f} non leggibile: ${e.message}`);
      return 1;
    }
  }

  const result = evaluate(findings, entries);
  for (const line of annotations(name, result, release)) console.log(line);
  const md = markdown(name, result, release);
  console.log(md);
  summary(md);
  return release && result.blocking.length > 0 ? 1 : 0;
}

if (import.meta.url === `file://${process.argv[1].replaceAll('\\', '/')}` || process.argv[1]?.endsWith('gate.mjs')) {
  process.exit(main(process.argv.slice(2)));
}
