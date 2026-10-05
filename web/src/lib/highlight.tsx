/*
 * Evidenziazione della sintassi condivisa (Markdown e vista file, M-04/H).
 * Libreria unica: lowlight (highlight.js, set "common"). Si carica con
 * import() dinamico, così non entra nel bundle delle pagine che non
 * mostrano codice. Il risultato è un albero hast convertito in elementi
 * React: niente dangerouslySetInnerHTML.
 */
import { createElement, type ReactNode } from 'react';

interface HastNode {
  type: string;
  value?: string;
  properties?: { className?: string[] };
  children?: HastNode[];
}

interface Lowlight {
  highlight(lang: string, code: string): { children: HastNode[] };
  registered(lang: string): boolean;
}

let loader: Promise<Lowlight> | null = null;

function loadLowlight(): Promise<Lowlight> {
  loader ??= import('lowlight').then((m) => m.createLowlight(m.common) as unknown as Lowlight);
  return loader;
}

function toReact(nodes: HastNode[], prefix = ''): ReactNode[] {
  return nodes.map((n, i) => {
    const key = `${prefix}${i}`;
    if (n.type === 'text') return n.value ?? '';
    if (n.type === 'element') {
      // Solo classi di token hljs-*, nient'altro.
      const cls = (n.properties?.className ?? []).filter((c) => /^hljs-[\w-]+$/.test(c)).join(' ');
      return createElement('span', { key, className: cls || undefined }, ...toReact(n.children ?? [], `${key}.`));
    }
    return null;
  });
}

/** Evidenzia `code`; con lingua sconosciuta o assente restituisce il testo semplice. */
export async function highlightCode(code: string, lang?: string): Promise<ReactNode[]> {
  const ll = await loadLowlight();
  const l = lang?.toLowerCase();
  if (!l || !ll.registered(l)) return [code];
  return toReact(ll.highlight(l, code).children);
}
