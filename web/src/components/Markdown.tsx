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

/**
 * Markdown in stile GitHub (GFM). L'HTML grezzo passa da rehype-raw e poi da
 * rehype-sanitize (allowlist di default, stile GitHub): niente script, on*,
 * iframe, form, style, svg, URL javascript:/data:. Gli id dei titoli si
 * aggiungono dopo la sanificazione.
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
        <a {...rest} href={href ? resolveRelative(href, basePath, resolveLink) : href}>
          {children}
        </a>
      );
    },
    img({ src, alt, node, ...rest }: WithNode<ComponentPropsWithoutRef<'img'>>) {
      void node;
      const s = typeof src === 'string' ? src : undefined;
      return <img {...rest} alt={alt ?? ''} src={s ? resolveRelative(s, basePath, resolveImage) : s} loading="lazy" />;
    },
    pre({ children }) {
      return <>{children}</>;
    },
    code({ className: cn, children, node, ...rest }: WithNode<ComponentPropsWithoutRef<'code'>>) {
      void node;
      const lang = /language-([\w+#.-]+)/.exec(cn ?? '')?.[1];
      const text = String(children ?? '');
      // Blocco: ha una lingua o termina con a capo (react-markdown non passa più `inline`).
      if (lang || text.endsWith('\n')) return <CodeBlock lang={lang} code={text.replace(/\n$/, '')} />;
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
        remarkRehypeOptions={{ allowDangerousHtml: true }}
        rehypePlugins={[rehypeRaw, rehypeSanitize, rehypeSlug]}
        components={components}
      >
        {source}
      </ReactMarkdown>
    </div>
  );
}
