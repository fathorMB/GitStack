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
export function Markdown({ source, basePath = '', resolveLink, resolveImage, className }: MarkdownProps) {
  const components: Components = {
    a({ href, children, node, ...rest }: WithNode<ComponentPropsWithoutRef<'a'>>) {
      void node;
      if (href && isExternal(href)) {
        return (
          <a {...rest} href={href} target="_blank" rel="noopener noreferrer">
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
      return <img {...rest} alt={alt ?? ''} src={s ? resolveRelative(s, basePath, resolveImage) : s} loading="lazy" />;
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
        rehypePlugins={[rehypeRaw, rehypeSlug, rehypeSanitize]}
        components={components}
      >
        {source}
      </ReactMarkdown>
    </div>
  );
}
