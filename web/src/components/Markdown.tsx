import { useEffect, useState, type ComponentPropsWithoutRef, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import rehypeSlug from 'rehype-slug';
import remarkGfm from 'remark-gfm';
import { highlightCode } from '../lib/highlight';

export interface MarkdownProps {
  source: string;
  /** Cartella (relativa alla radice del repo) del file che contiene il Markdown, es. "docs". */
  basePath?: string;
  /** Percorso relativo alla radice del repo -> URL della pagina (blob/tree). */
  resolveLink?: (repoPath: string) => string;
  /** Percorso relativo alla radice del repo -> URL dell'immagine (raw). */
  resolveImage?: (repoPath: string) => string;
  /** Repo corrente: abilita #n; senza, #n resta testo (owner/repo#n funziona comunque). */
  repo?: { owner: string; name: string };
  className?: string;
}

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

/** Normalizza un percorso relativo rispetto a basePath; "/x" parte dalla radice del repo. */
function repoPathFor(href: string, basePath = ''): { path: string; suffix: string } {
  const m = /^([^?#]*)(.*)$/.exec(href);
  const raw = m?.[1] ?? '';
  const parts: string[] = raw.startsWith('/') ? [] : basePath.split('/').filter(Boolean);
  for (const seg of raw.split('/')) {
    if (seg === '' || seg === '.') continue;
    if (seg === '..') parts.pop();
    else parts.push(seg);
  }
  return { path: parts.join('/'), suffix: m?.[2] ?? '' };
}

function isExternal(href: string): boolean {
  return /^(https?:)?\/\//i.test(href);
}

function resolveRelative(href: string, basePath: string, fn?: (p: string) => string): string {
  if (!fn || href === '' || href.startsWith('#') || SCHEME.test(href) || href.startsWith('//')) return href;
  const { path, suffix } = repoPathFor(href, basePath);
  return fn(path) + suffix;
}

interface HastNode {
  type: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
}

const REF_SKIP = new Set(['code', 'pre', 'a', 'script', 'style']);
// owner/repo#n | #n | @utente | @org/team (non dentro parole, URL o email).
const REF_RE =
  /(?<![\w/&.@#-])(?:([A-Za-z0-9][\w.-]*)\/([\w.-]+))?#(\d+)(?![\w-])|(?<![\w@/.+-])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))(?:\/([A-Za-z0-9](?:[\w.-]*[A-Za-z0-9_-])?))?(?![\w@-])/g;

function refLink(href: string, text: string): HastNode {
  return { type: 'element', tagName: 'a', properties: { href, dataRef: 'true' }, children: [{ type: 'text', value: text }] };
}

/** Dopo la sanificazione: trasforma i riferimenti nei nodi di testo (mai in code/pre/a). */
function linkRefs(node: HastNode, repo?: { owner: string; name: string }): void {
  if (!node.children) return;
  const out: HastNode[] = [];
  for (const child of node.children) {
    if (child.type === 'element' && child.tagName && REF_SKIP.has(child.tagName)) {
      out.push(child);
      continue;
    }
    if (child.type !== 'text' || !child.value) {
      linkRefs(child, repo);
      out.push(child);
      continue;
    }
    const text = child.value;
    let last = 0;
    for (const m of text.matchAll(REF_RE)) {
      let a: HastNode | null = null;
      if (m[3]) {
        if (m[1] && m[2]) a = refLink(`/${m[1]}/${m[2]}/issues/${m[3]}`, m[0]);
        else if (repo) a = refLink(`/${repo.owner}/${repo.name}/issues/${m[3]}`, m[0]);
      } else if (m[4]) {
        a = refLink(m[5] ? `/orgs/${m[4]}/teams/${m[5]}` : `/${m[4]}`, m[0]);
      }
      if (!a) continue;
      if (m.index > last) out.push({ type: 'text', value: text.slice(last, m.index) });
      out.push(a);
      last = m.index + m[0].length;
    }
    if (last === 0) out.push(child);
    else if (last < text.length) out.push({ type: 'text', value: text.slice(last) });
  }
  node.children = out;
}

function CodeBlock({ lang, code }: { lang?: string; code: string }) {
  const [nodes, setNodes] = useState<ReactNode[] | null>(null);
  useEffect(() => {
    let live = true;
    highlightCode(code, lang).then(
      (n) => {
        if (live) setNodes(n);
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [code, lang]);
  return (
    <pre className="md-pre">
      <code className={lang ? `language-${lang}` : undefined}>{nodes ?? code}</code>
    </pre>
  );
}

type WithNode<T> = T & { node?: unknown };

interface HastLike {
  type: string;
  tagName?: string;
  value?: string;
  properties?: { className?: unknown };
  children?: HastLike[];
}

function hastText(n: HastLike): string {
  return n.type === 'text' ? (n.value ?? '') : (n.children ?? []).map(hastText).join('');
}

const CLOBBER = 'user-content-';

/** #sez -> #user-content-sez (gli id dei titoli hanno il prefisso). */
function anchorHref(href: string): string {
  return href.startsWith('#') && href.length > 1 && !href.startsWith('#' + CLOBBER) ? '#' + CLOBBER + href.slice(1) : href;
}

/**
 * Markdown in stile GitHub (GFM). L'HTML grezzo passa da rehype-raw e poi da
 * rehype-sanitize (allowlist di default, stile GitHub): niente script, on*,
 * iframe, form, style, svg, URL javascript:/data:. Gli id dei titoli (rehype-slug)
 * sono generati prima della sanificazione, che li prefissa con `user-content-`
 * (anti DOM clobbering); le ancore `#sez` sono riscritte di conseguenza.
 */
export function Markdown({ source, basePath = '', resolveLink, resolveImage, repo, className }: MarkdownProps) {
  const components: Components = {
    a({ href, children, node, ...rest }: WithNode<ComponentPropsWithoutRef<'a'>>) {
      void node;
      if ((rest as Record<string, unknown>)['data-ref']) {
        return (
          <a href={href} className="md-ref">
            {children}
          </a>
        );
      }
      if (href && isExternal(href)) {
        return (
          <a {...rest} href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">
            {children}
          </a>
        );
      }
      return (
        <a {...rest} href={href ? anchorHref(resolveRelative(href, basePath, resolveLink)) : href}>
          {children}
        </a>
      );
    },
    img({ src, alt, node, ...rest }: WithNode<ComponentPropsWithoutRef<'img'>>) {
      void node;
      const s = typeof src === 'string' ? src : undefined;
      return <img {...rest} alt={alt ?? ''} src={s ? resolveRelative(s, basePath, resolveImage) : s} loading="lazy" referrerPolicy="no-referrer" />;
    },
    pre({ children, node, ...rest }: WithNode<ComponentPropsWithoutRef<'pre'>>) {
      // <pre><code> = blocco di codice (CodeBlock, evidenziato); un <pre> HTML
      // senza code resta un pre, così la spaziatura (ASCII art) si conserva.
      const first = (node as HastLike | undefined)?.children?.[0];
      if (first?.type === 'element' && first.tagName === 'code') {
        const cn = first.properties?.className;
        const lang = (Array.isArray(cn) ? cn.join(' ') : String(cn ?? '')).match(/language-([\w+#.-]+)/)?.[1];
        return <CodeBlock lang={lang} code={hastText(first).replace(/\n$/, '')} />;
      }
      return (
        <pre {...rest} className="md-pre">
          {children}
        </pre>
      );
    },
    code({ className: cn, children, node, ...rest }: WithNode<ComponentPropsWithoutRef<'code'>>) {
      void node;
      return (
        <code {...rest} className={cn}>
          {children}
        </code>
      );
    },
  };
  return (
    <div className={`md${className ? ` ${className}` : ''}`}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        remarkRehypeOptions={{ allowDangerousHtml: true, clobberPrefix: '' }}
        rehypePlugins={[rehypeRaw, rehypeSlug, rehypeSanitize, () => (tree: HastNode) => linkRefs(tree, repo)]}
        components={components}
      >
        {source}
      </ReactMarkdown>
    </div>
  );
}
