// Parsing di un patch unificato (solo hunk, come lo da' l'API) in righe
// pronte da disegnare: vista unificata e affiancata (B6).

export type DiffLineKind = 'hunk' | 'ctx' | 'add' | 'del' | 'note';

export interface DiffLine {
  kind: DiffLineKind;
  /** Numero di riga nel vecchio file (assente per add/hunk). */
  oldNo?: number;
  /** Numero di riga nel nuovo file (assente per del/hunk). */
  newNo?: number;
  /** Testo senza il prefisso +/-/spazio. */
  text: string;
}

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

export function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;
  const lines = patch.split('\n');
  if (lines[lines.length - 1] === '') lines.pop();
  for (const raw of lines) {
    const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw;
    const m = HUNK.exec(line);
    if (m) {
      oldNo = Number(m[1]);
      newNo = Number(m[2]);
      out.push({ kind: 'hunk', text: line });
    } else if (line.startsWith('+')) {
      out.push({ kind: 'add', newNo: newNo++, text: line.slice(1) });
    } else if (line.startsWith('-')) {
      out.push({ kind: 'del', oldNo: oldNo++, text: line.slice(1) });
    } else if (line.startsWith('\\')) {
      out.push({ kind: 'note', text: line });
    } else if (line.startsWith(' ') || line === '') {
      out.push({ kind: 'ctx', oldNo: oldNo++, newNo: newNo++, text: line.slice(1) });
    }
    // Altre intestazioni (diff --git, index, ---/+++) non fanno parte degli hunk.
  }
  return out;
}

export interface SplitRow {
  left: DiffLine | null;
  right: DiffLine | null;
  /** Riga a tutta larghezza (hunk, nota). */
  span?: DiffLine;
}

/** Affianca le righe: un blocco di del seguito da add si allinea riga per riga. */
export function toSplitRows(lines: readonly DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const l = lines[i]!;
    if (l.kind === 'hunk' || l.kind === 'note') {
      rows.push({ left: null, right: null, span: l });
      i++;
    } else if (l.kind === 'ctx') {
      rows.push({ left: l, right: l });
      i++;
    } else {
      const dels: DiffLine[] = [];
      const adds: DiffLine[] = [];
      while (i < lines.length && lines[i]!.kind === 'del') dels.push(lines[i++]!);
      while (i < lines.length && lines[i]!.kind === 'add') adds.push(lines[i++]!);
      const n = Math.max(dels.length, adds.length);
      for (let k = 0; k < n; k++) rows.push({ left: dels[k] ?? null, right: adds[k] ?? null });
    }
  }
  return rows;
}
