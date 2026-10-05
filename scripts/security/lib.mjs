// Libreria del gate di sicurezza (GIT-114): legge i report degli strumenti,
// toglie le voci coperte da un'eccezione ancora valida e decide se il job
// deve fallire. Nessuna dipendenza npm: gira con il Node dei runner.
//
// Modello comune: ogni strumento produce "finding" {tool, id, severity,
// title, location}. Le severità sono CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN.

import fs from 'node:fs';

export const BLOCKING = new Set(['CRITICAL', 'HIGH']);
const SEVERITIES = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN'];

// ---------- eccezioni ----------

// Valida il file e restituisce {entries, problems}. Una voce vale se ha id,
// tool, motivo non vuoto e scadenza AAAA-MM-GG; è "scaduta" dal giorno
// stesso della scadenza (la data è il primo giorno in cui non vale più).
export function loadExceptions(file, today = new Date()) {
  const problems = [];
  let raw;
  try {
    raw = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch (e) {
    return { entries: [], problems: [`${file}: non leggibile come JSON (${e.message})`] };
  }
  const list = Array.isArray(raw.exceptions) ? raw.exceptions : null;
  if (!list) return { entries: [], problems: [`${file}: manca l'array "exceptions"`] };
  const day = today.toISOString().slice(0, 10);
  const entries = list.map((e, i) => {
    const where = `${file} voce ${i + 1}${e && e.id ? ` (${e.id})` : ''}`;
    for (const k of ['id', 'tool', 'reason', 'expires']) {
      if (typeof e?.[k] !== 'string' || e[k].trim() === '') problems.push(`${where}: campo "${k}" mancante`);
    }
    if (typeof e?.expires === 'string' && !/^\d{4}-\d{2}-\d{2}$/.test(e.expires)) {
      problems.push(`${where}: "expires" deve essere AAAA-MM-GG`);
    }
    return { ...e, expired: typeof e?.expires === 'string' && e.expires <= day };
  });
  return { entries, problems };
}

// Una voce copre un finding se tool e id coincidono e, quando c'è "path", la
// posizione del finding lo contiene. Le voci scadute non coprono nulla.
export function isExcepted(finding, entries) {
  return entries.some(
    (e) =>
      !e.expired &&
      e.tool === finding.tool &&
      e.id === finding.id &&
      (!e.path || String(finding.location || '').includes(e.path)),
  );
}

// ---------- parser dei report ----------

function normSeverity(s) {
  const u = String(s || '').toUpperCase();
  if (u === 'MODERATE') return 'MEDIUM';
  return SEVERITIES.includes(u) ? u : 'UNKNOWN';
}

// govulncheck -format json emette oggetti JSON concatenati, non un array.
export function splitJsonStream(text) {
  const out = [];
  let depth = 0;
  let start = -1;
  let inStr = false;
  let esc = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inStr) {
      if (esc) esc = false;
      else if (c === '\\') esc = true;
      else if (c === '"') inStr = false;
      continue;
    }
    if (c === '"') inStr = true;
    else if (c === '{') {
      if (depth === 0) start = i;
      depth++;
    } else if (c === '}') {
      depth--;
      if (depth === 0) out.push(JSON.parse(text.slice(start, i + 1)));
    }
  }
  return out;
}

// govulncheck non assegna una gravità: conta solo la vulnerabilità
// raggiungibile (trace con "function", cioè il codice la chiama davvero) e
// la tratta come HIGH. Le altre (solo nel modulo o nel pacchetto importato,
// non chiamate) sono MEDIUM, mai bloccanti. L'id è l'id GO-... dell'OSV.
export function parseGovulncheck(text, label = '') {
  const objs = splitJsonStream(text);
  const osv = new Map(objs.filter((o) => o.osv).map((o) => [o.osv.id, o.osv]));
  const seen = new Map();
  for (const o of objs) {
    if (!o.finding) continue;
    const f = o.finding;
    const first = f.trace?.[0] || {};
    const called = Boolean(first.function);
    const key = `${f.osv}|${first.module}`;
    const prev = seen.get(key);
    if (prev && (prev.severity === 'HIGH' || !called)) continue;
    const info = osv.get(f.osv) || {};
    seen.set(key, {
      tool: 'govulncheck',
      id: f.osv,
      severity: called ? 'HIGH' : 'MEDIUM',
      title: `${info.summary || f.osv} (${first.module}${first.version ? '@' + first.version : ''}${f.fixed_version ? `, corretto in ${f.fixed_version}` : ''}${called ? ', raggiungibile' : ', non chiamata'})`,
      location: label || first.module || '',
    });
  }
  return [...seen.values()];
}

export function parseGosec(text) {
  const j = JSON.parse(text);
  return (j.Issues || []).map((i) => ({
    tool: 'gosec',
    id: i.rule_id,
    severity: normSeverity(i.severity),
    title: `${i.details}`,
    location: `${relPath(i.file)}:${i.line}`,
  }));
}

function relPath(p) {
  const cwd = process.cwd().replaceAll('\\', '/') + '/';
  const norm = String(p || '').replaceAll('\\', '/');
  return norm.startsWith(cwd) ? norm.slice(cwd.length) : norm;
}

// eslint -f json: severity 2 = error (HIGH), 1 = warning (MEDIUM). Gli errori
// di parsing (ruleId null) non sono finding di sicurezza e si ignorano.
export function parseEslint(text) {
  const j = JSON.parse(text);
  const out = [];
  for (const f of j) {
    for (const m of f.messages || []) {
      if (!m.ruleId) continue;
      out.push({
        tool: 'eslint-security',
        id: m.ruleId,
        severity: m.severity === 2 ? 'HIGH' : 'MEDIUM',
        title: m.message,
        location: `${relPath(f.filePath)}:${m.line}`,
      });
    }
  }
  return out;
}

// pnpm audit --json: {advisories:{<id>:{github_advisory_id, severity,
// module_name, title, findings:[{version}]}}}. L'id dell'eccezione è il GHSA.
export function parsePnpmAudit(text) {
  const j = JSON.parse(text);
  return Object.values(j.advisories || {}).map((a) => ({
    tool: 'pnpm-audit',
    id: a.github_advisory_id || String(a.id),
    severity: normSeverity(a.severity),
    title: `${a.title} (${a.module_name}${a.findings?.[0]?.version ? '@' + a.findings[0].version : ''})`,
    location: 'web/pnpm-lock.yaml',
  }));
}

// trivy image -f json: Results[].Vulnerabilities[]. Un'immagine per file.
export function parseTrivy(text, label = '') {
  const j = JSON.parse(text);
  const out = [];
  for (const r of j.Results || []) {
    for (const v of r.Vulnerabilities || []) {
      out.push({
        tool: 'trivy',
        id: v.VulnerabilityID,
        severity: normSeverity(v.Severity),
        title: `${v.Title || v.VulnerabilityID} (${v.PkgName}@${v.InstalledVersion}${v.FixedVersion ? `, corretto in ${v.FixedVersion}` : ', nessuna correzione'})`,
        location: label || j.ArtifactName || r.Target,
      });
    }
  }
  return out;
}

// ---------- decisione ----------

// Restituisce {active, excepted, blocking}: "blocking" sono i finding non
// coperti con gravità CRITICAL/HIGH.
export function evaluate(findings, entries) {
  const active = [];
  const excepted = [];
  for (const f of findings) (isExcepted(f, entries) ? excepted : active).push(f);
  return { active, excepted, blocking: active.filter((f) => BLOCKING.has(f.severity)) };
}

export function sortFindings(list) {
  return [...list].sort(
    (a, b) => SEVERITIES.indexOf(a.severity) - SEVERITIES.indexOf(b.severity) || a.id.localeCompare(b.id),
  );
}

// Per la lettura: lo stesso id e la stessa gravità trovati in più posti
// (moduli, immagini) diventano una riga sola con l'elenco dei posti.
export function groupFindings(list) {
  const m = new Map();
  for (const f of sortFindings(list)) {
    const k = `${f.tool}|${f.id}|${f.severity}`;
    const g = m.get(k);
    if (g) g.locations.push(f.location);
    else m.set(k, { ...f, locations: [f.location] });
  }
  return [...m.values()].map((g) => ({ ...g, location: [...new Set(g.locations)].join(', ') }));
}

const cell = (s) => String(s).replaceAll('|', '\\|').replaceAll('\n', ' ');

export function markdown(name, { active, excepted, blocking }, release) {
  const lines = [`### ${name}`, ''];
  const counts = SEVERITIES.map((s) => [s, active.filter((f) => f.severity === s).length]).filter(([, n]) => n);
  lines.push(
    active.length === 0
      ? 'Nessun finding.'
      : `${active.length} finding (${counts.map(([s, n]) => `${s}: ${n}`).join(', ')}).`,
  );
  if (excepted.length) lines.push(`${excepted.length} finding coperti da un'eccezione valida.`);
  lines.push(
    release
      ? blocking.length
        ? `**BLOCCANTE (release): ${blocking.length} finding CRITICAL/HIGH senza eccezione.**`
        : 'Release: nessun finding CRITICAL/HIGH senza eccezione.'
      : 'Modalità segnalazione: non blocca (blocca solo sui tag `v*`).',
  );
  const groups = groupFindings(active);
  if (groups.length) {
    lines.push('', `${groups.length} voci distinte.`, '', '| Gravità | Id | Dove | Descrizione |', '|---|---|---|---|');
    for (const f of groups.slice(0, 100)) {
      lines.push(`| ${f.severity} | ${cell(f.id)} | ${cell(f.location)} | ${cell(f.title)} |`);
    }
    if (groups.length > 100) lines.push('', `… altre ${groups.length - 100} voci non mostrate.`);
  }
  return lines.join('\n') + '\n';
}

// Annotazioni GitHub: warning in segnalazione, error sui bloccanti in release.
export function annotations(name, { active }, release) {
  return groupFindings(active)
    .slice(0, 50)
    .map((f) => {
      const level = release && BLOCKING.has(f.severity) ? 'error' : 'warning';
      const esc = (s) => String(s).replaceAll('%', '%25').replaceAll('\r', '%0D').replaceAll('\n', '%0A');
      return `::${level} title=${esc(`${name}: ${f.id} (${f.severity})`)}::${esc(`${f.location} - ${f.title}`)}`;
    });
}
