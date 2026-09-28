#!/usr/bin/env node
// Genera web/src/styles/tokens.css a partire dai token CSS della styleguide
// di riferimento (.lmbrain-lite/design/styleguide/index.html): i token si
// importano, non si ricopiano a mano (criterio di accettazione di GIT-7,
// nota di rework del CTO). Nessuna dipendenza npm: solo Node.
//
// Uso, da web/ (o dalla radice, vedi web/package.json → "sync-tokens"):
//   node scripts/sync-tokens.mjs
//
// In CI il job "ts" (.github/workflows/ci.yml) rilancia questo script e
// fallisce se src/styles/tokens.css committato non è allineato
// (git diff --exit-code), sullo stesso principio di
// scripts/check-api-generated.sh per il client TS/Go generato dal
// contratto OpenAPI.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
const styleguidePath = join(repoRoot, '.lmbrain-lite', 'design', 'styleguide', 'index.html');
const outPath = join(here, '..', 'src', 'styles', 'tokens.css');

const source = readFileSync(styleguidePath, 'utf8');

/**
 * Estrae un blocco CSS bilanciato (contando le graffe) a partire dalla
 * prima occorrenza letterale di `marker` in `text`. Il marker è incluso
 * nell'output, così il blocco resta un frammento CSS valido e completo
 * (selettore + corpo, comprese eventuali regole annidate come @media).
 */
function extractBlock(text, marker) {
  const markerIndex = text.indexOf(marker);
  if (markerIndex === -1) {
    throw new Error(`Marker non trovato in ${styleguidePath}: ${JSON.stringify(marker)}`);
  }
  const openIndex = text.indexOf('{', markerIndex);
  if (openIndex === -1) {
    throw new Error(`Nessuna graffa di apertura dopo il marker: ${JSON.stringify(marker)}`);
  }
  let depth = 0;
  for (let i = openIndex; i < text.length; i++) {
    if (text[i] === '{') {
      depth++;
    } else if (text[i] === '}') {
      depth--;
      if (depth === 0) {
        return text.slice(markerIndex, i + 1);
      }
    }
  }
  throw new Error(`Graffa di chiusura non trovata per il marker: ${JSON.stringify(marker)}`);
}

// ':root{' (non ':root:not(...)' né ':root[data-theme=...]'): il blocco
// base dei token, tema chiaro.
const lightBlock = extractBlock(source, ':root{');
// Tema scuro via prefers-color-scheme (quando l'utente non ha forzato
// data-theme="light").
const darkMediaBlock = extractBlock(source, '@media (prefers-color-scheme:dark)');
// Tema scuro forzato via attributo (toggle esplicito, non solo preferenza
// del sistema).
const darkAttrBlock = extractBlock(source, ':root[data-theme="dark"]{');

const header = `/*
 * Design tokens "Aurora" di GitStack — GENERATO, non modificare a mano.
 *
 * Estratto da .lmbrain-lite/design/styleguide/index.html (styleguide di
 * riferimento, sezione "DESIGN TOKENS · palette Aurora") con
 * web/scripts/sync-tokens.mjs. Il resto del CSS di web/ usa sempre
 * var(--token), mai un valore fisso: se la styleguide cambia, rigenerare
 * con:
 *
 *   node web/scripts/sync-tokens.mjs
 *
 * (o, dentro web/: pnpm run sync-tokens). Il job "ts" della CI rilancia
 * questo script e fallisce se questo file committato non è allineato.
 */
`;

const css = `${header}\n${lightBlock}\n\n${darkMediaBlock}\n\n${darkAttrBlock}\n`;

writeFileSync(outPath, css);
console.log(`Scritto ${outPath} da ${styleguidePath}`);
