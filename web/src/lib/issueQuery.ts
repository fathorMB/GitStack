// Ritocchi alla barra di ricerca delle issues (sintassi I10, pkg/issuequery).
// Non e' un parser completo: spezza la stringa in token (rispettando le
// virgolette) e legge/scrive i qualificatori; la validita' la decide l'API.

export type Qualifier = 'is' | 'reason' | 'label' | 'assignee' | 'author' | 'milestone' | 'no';

/** Spezza la ricerca in token separati da spazi, con le virgolette intatte. */
export function tokenize(q: string): string[] {
  const out: string[] = [];
  let cur = '';
  let quoted = false;
  for (let i = 0; i < q.length; i++) {
    const c = q[i];
    if (c === '\\' && quoted && i + 1 < q.length) {
      cur += c + q[++i];
      continue;
    }
    if (c === '"') quoted = !quoted;
    if (!quoted && /\s/.test(c)) {
      if (cur) out.push(cur);
      cur = '';
    } else cur += c;
  }
  if (cur) out.push(cur);
  return out;
}

function unquote(v: string): string {
  if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) return v.slice(1, -1).replace(/\\(["\\])/g, '$1');
  return v;
}

export function quoteValue(v: string): string {
  return /[\s"\\]/.test(v) || v === '' ? `"${v.replace(/(["\\])/g, '\\$1')}"` : v;
}

function keyOf(token: string): { neg: boolean; key: string; value: string } | null {
  const m = /^(-?)([A-Za-z]+):(.*)$/s.exec(token);
  return m ? { neg: m[1] === '-', key: m[2].toLowerCase(), value: unquote(m[3]) } : null;
}

/** Valori (non negati) di un qualificatore, nell'ordine di scrittura. */
export function qualifierValues(q: string, key: Qualifier): string[] {
  return tokenize(q).flatMap((t) => {
    const k = keyOf(t);
    return k && !k.neg && k.key === key ? [k.value] : [];
  });
}

/** Sostituisce tutte le occorrenze (non negate) del qualificatore con `value`, o le toglie se vuoto. */
export function setQualifier(q: string, key: Qualifier, value: string | null): string {
  const kept = tokenize(q).filter((t) => {
    const k = keyOf(t);
    return !(k && !k.neg && k.key === key);
  });
  if (value) kept.push(`${key}:${quoteValue(value)}`);
  return kept.join(' ');
}

/** True se la ricerca ha testo libero (serve per `sort=relevance`). */
export function hasFreeText(q: string): boolean {
  return tokenize(q).some((t) => keyOf(t) === null);
}

export const DEFAULT_QUERY = 'is:open';

/** Barra mostrata: con il cursore pronto per scrivere altro. */
export function withTrailingSpace(q: string): string {
  return q && !q.endsWith(' ') ? `${q} ` : q;
}
