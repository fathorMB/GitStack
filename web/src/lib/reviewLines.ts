// Supporto alla review sul diff: ancore delle righe, blocchi suggestion, intervalli.
import type { DiffSide, PullRequestThread } from './pullsApi';
import type { DiffLine } from './diffParse';

export interface Anchor {
  side: DiffSide;
  no: number;
}

/** Riga commentabile: `add` e `ctx` stanno sul lato new, `del` sul lato old; hunk e note no. */
export function anchorOf(l: DiffLine): Anchor | null {
  if (l.kind === 'del' && l.oldNo !== undefined) return { side: 'old', no: l.oldNo };
  if ((l.kind === 'add' || l.kind === 'ctx') && l.newNo !== undefined) return { side: 'new', no: l.newNo };
  return null;
}

export const anchorKey = (side: DiffSide, no: number) => `${side}:${no}`;

/** Testo del lato: `+90` per new, `−90` per old, come nel mockup. */
export const lineLabel = (side: DiffSide, no: number) => `${side === 'new' ? '+' : '−'}${no}`;

export function rangeLabel(side: DiffSide, start: number | null | undefined, line: number): string {
  return start != null && start < line ? `lines ${lineLabel(side, start)} to ${lineLabel(side, line)}` : `line ${lineLabel(side, line)}`;
}

export type BodyPart = { kind: 'text'; text: string } | { kind: 'suggestion'; code: string[] };

const FENCE = /^```suggestion[ \t]*$/;

/** Divide un commento in testo e blocchi ```suggestion (PR5): il blocco si mostra come diff. */
export function splitSuggestions(body: string): BodyPart[] {
  const lines = body.replace(/\r\n/g, '\n').split('\n');
  const parts: BodyPart[] = [];
  let text: string[] = [];
  let i = 0;
  const flush = () => {
    const t = text.join('\n');
    if (t.trim()) parts.push({ kind: 'text', text: t });
    text = [];
  };
  while (i < lines.length) {
    const line = lines[i]!;
    if (FENCE.test(line)) {
      const end = lines.findIndex((x, k) => k > i && x.trim() === '```');
      if (end !== -1) {
        flush();
        parts.push({ kind: 'suggestion', code: lines.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }
    text.push(line);
    i++;
  }
  flush();
  return parts;
}

/** Righe del diff nell'intervallo di una conversazione, sul suo lato. */
export function linesInRange(lines: readonly DiffLine[], t: Pick<PullRequestThread, 'side' | 'line' | 'startLine'>): DiffLine[] {
  const from = t.startLine ?? t.line;
  return lines.filter((l) => {
    const a = anchorOf(l);
    return a !== null && a.side === t.side && a.no >= from && a.no <= t.line;
  });
}
