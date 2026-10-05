import { cloneElement, isValidElement } from 'react';
import type { ReactElement, ReactNode } from 'react';

/** Spezza un albero di nodi React su 
: ogni elemento e' ricreato in ogni riga che attraversa. */
export function splitLines(nodes: ReactNode[], prefix = ''): ReactNode[][] {
  const lines: ReactNode[][] = [[]];
  const last = () => lines[lines.length - 1] as ReactNode[];
  nodes.forEach((node, i) => {
    if (typeof node === 'string') {
      node.split('\n').forEach((part, j) => {
        if (j > 0) lines.push([]);
        if (part !== '') last().push(part);
      });
    } else if (isValidElement(node)) {
      const el = node as ReactElement<{ children?: ReactNode }>;
      const c = el.props.children;
      const kids = Array.isArray(c) ? (c as ReactNode[]) : c === undefined || c === null ? [] : [c];
      splitLines(kids, `${prefix}${i}.`).forEach((part, j) => {
        if (j > 0) lines.push([]);
        if (part.length > 0) last().push(cloneElement(el, { key: `${prefix}${i}-${j}` }, ...part));
      });
    }
  });
  return lines;
}

/** Righe di testo di un file: l'eventuale newline finale non genera una riga vuota. */
export function textLines(content: string): string[] {
  const lines = content.split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

/** `#L10` o `#L10-L20` -> [10, 10] | [10, 20]; null se non e' un'ancora di riga. */
export function parseLineHash(hash: string): [number, number] | null {
  const m = /^#L(\d+)(?:-L?(\d+))?$/.exec(hash);
  if (!m) return null;
  const a = Number(m[1]);
  const b = m[2] ? Number(m[2]) : a;
  return a <= b ? [a, b] : [b, a];
}

export function lineHash(a: number, b: number): string {
  return a === b ? `#L${a}` : `#L${Math.min(a, b)}-L${Math.max(a, b)}`;
}
