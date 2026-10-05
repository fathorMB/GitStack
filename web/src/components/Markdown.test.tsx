import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Markdown } from './Markdown';

const resolveLink = (p: string) => `/o/r/blob/main/${p}`;
const resolveImage = (p: string) => `/raw/o/r/main/${p}`;

function renderMd(source: string, props: Partial<Parameters<typeof Markdown>[0]> = {}) {
  return render(<Markdown source={source} resolveLink={resolveLink} resolveImage={resolveImage} {...props} />);
}

describe('Markdown: sicurezza', () => {
  it('neutralizza i payload XSS noti', () => {
    const payloads = [
      '<script>window.__x=1</script>',
      '<img src="x" onerror="window.__x=1">',
      '[click](javascript:alert(1))',
      '<a href="JaVaScRiPt:alert(1)">mixed</a>',
      '<a href="data:text/html,<script>alert(1)</script>">data</a>',
      '<svg onload="alert(1)"><script>alert(1)</script></svg>',
      '<iframe src="https://evil.example"></iframe>',
      '<div style="background:url(https://evil.example/x)">s</div>',
      '<form action="https://evil.example"><input name="a"><button>go</button></form>',
      '<p onclick="alert(1)">p</p>',
    ];
    const { container } = renderMd(payloads.join('\n\n'));
    const html = container.innerHTML;
    expect(container.querySelector('script, iframe, svg, form, style')).toBeNull();
    expect(html).not.toMatch(/\son\w+=/i);
    expect(html).not.toMatch(/javascript:/i);
    expect(html).not.toMatch(/data:text/i);
    expect(html).not.toMatch(/url\(/i);
    expect(html).not.toContain('evil.example');
    expect((window as unknown as { __x?: number }).__x).toBeUndefined();
  });

  it('i link esterni hanno rel noopener noreferrer', () => {
    renderMd('[ext](https://example.com/a)');
    const a = screen.getByRole('link', { name: 'ext' });
    expect(a).toHaveAttribute('href', 'https://example.com/a');
    expect(a).toHaveAttribute('rel', 'noopener noreferrer');
    expect(a).toHaveAttribute('target', '_blank');
  });
});

describe('Markdown: GFM', () => {
  it('rende tabelle, liste di attività, autolink e ancore sui titoli', () => {
    const { container } = renderMd(
      '## Titolo uno\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] fatto\n- [ ] da fare\n\nhttps://example.org/x\n',
    );
    expect(container.querySelector('table th')).toHaveTextContent('a');
    const boxes = container.querySelectorAll('input[type="checkbox"]');
    expect(boxes).toHaveLength(2);
    expect((boxes[0] as HTMLInputElement).checked).toBe(true);
    expect((boxes[1] as HTMLInputElement).checked).toBe(false);
    expect(screen.getByRole('link', { name: 'https://example.org/x' })).toBeInTheDocument();
    expect(container.querySelector('h2')).toHaveAttribute('id', 'user-content-titolo-uno');
  });

  it('codice delimitato con lingua, evidenziato', async () => {
    const { container } = renderMd('```go\nfunc main() {}\n```\n');
    const code = container.querySelector('pre code');
    expect(code).toHaveClass('language-go');
    await waitFor(() => expect(container.querySelector('pre code .hljs-keyword')).not.toBeNull(), { timeout: 20000 });
    expect(code).toHaveTextContent('func main() {}');
  }, 30000);

  it('codice con lingua sconosciuta resta testo', async () => {
    const { container } = renderMd('```nonesiste\n<b>x</b>\n```\n');
    await waitFor(() => expect(container.querySelector('pre code')).toHaveTextContent('<b>x</b>'));
    expect(container.querySelector('pre code b')).toBeNull();
  });
});

describe('Markdown: id e pre', () => {
  it('gli id dei titoli hanno il prefisso e non collidono con la pagina', () => {
    const { container } = renderMd('# root\n\n[vai](#root)');
    expect(container.querySelector('[id="root"]')).toBeNull();
    expect(container.querySelector('h1')).toHaveAttribute('id', 'user-content-root');
    expect(screen.getByRole('link', { name: 'vai' })).toHaveAttribute('href', '#user-content-root');
  });

  it('un <pre> HTML grezzo resta un pre con la spaziatura', () => {
    const { container } = renderMd('<pre>a\n  b</pre>');
    const pre = container.querySelector('pre');
    expect(pre).not.toBeNull();
    expect(pre?.textContent).toBe('a\n  b');
  });
});

describe('Markdown: percorsi relativi', () => {
  it('risolve link e immagini nel repo e nel ref', () => {
    const { container } = renderMd(
      '[a](./x.md) [b](../y.md#sez) [c](/z/w.md) [d](#top) ![i](img/p.png) ![j](/img/q.png) ![k](https://e.com/i.png)',
      { basePath: 'docs/guide' },
    );
    const href = (name: string) => screen.getByRole('link', { name }).getAttribute('href');
    expect(href('a')).toBe('/o/r/blob/main/docs/guide/x.md');
    expect(href('b')).toBe('/o/r/blob/main/docs/y.md#sez');
    expect(href('c')).toBe('/o/r/blob/main/z/w.md');
    expect(href('d')).toBe('#user-content-top');
    const srcs = Array.from(container.querySelectorAll('img')).map((i) => i.getAttribute('src'));
    expect(srcs).toEqual(['/raw/o/r/main/docs/guide/img/p.png', '/raw/o/r/main/img/q.png', 'https://e.com/i.png']);
  });
});

describe('Markdown: note a piè di pagina', () => {
  it('rimando, nota e link di ritorno puntano agli id giusti', () => {
    const { container } = renderMd('Testo[^1]\n\n[^1]: nota');
    const ref = container.querySelector('sup a');
    const back = container.querySelector('li a[href^="#"]');
    const note = container.querySelector('li');
    expect(ref).not.toBeNull();
    expect(note?.id).toBe('user-content-fn-1');
    expect(ref?.getAttribute('href')).toBe('#' + note?.id);
    expect(ref?.id).toBeTruthy();
    expect(back?.getAttribute('href')).toBe('#' + ref?.id);
    const ids = Array.from(container.querySelectorAll('[id]')).map((e) => e.id);
    expect(ids.length).toBeGreaterThan(0);
    expect(ids.some((i) => i.includes('user-content-user-content-'))).toBe(false);
    expect(container.innerHTML).not.toContain('user-content-user-content-');
  });
});

describe('Markdown: riferimenti e @menzioni (B2)', () => {
  const repo = { owner: 'acme', name: 'app' };
  const hrefs = (c: HTMLElement) => Array.from(c.querySelectorAll('a')).map((a) => a.getAttribute('href'));

  it('#n, owner/repo#n e @utente diventano link', () => {
    const { container } = renderMd('Vedi #12, other/lib#7 e @mario, @acme/devs.', { repo });
    expect(hrefs(container)).toEqual(['/acme/app/issues/12', '/other/lib/issues/7', '/mario', '/orgs/acme/teams/devs']);
    expect(container.querySelector('a')).toHaveTextContent('#12');
    expect(container.textContent).toBe('Vedi #12, other/lib#7 e @mario, @acme/devs.');
  });

  it('senza repo corrente #n resta testo', () => {
    const { container } = renderMd('Vedi #12 e x/y#3');
    expect(hrefs(container)).toEqual(['/x/y/issues/3']);
  });

  it('niente riferimenti in codice inline e blocchi', () => {
    const { container } = renderMd('`#1 @a o/r#2`\n\n```\n#3 @b o/r#4\n```\n\n<pre>#5 @c</pre>', { repo });
    expect(container.querySelectorAll('a')).toHaveLength(0);
  });

  it('niente link dentro link, email o URL', () => {
    const { container } = renderMd('[#1](https://x.example) a@b.com https://x.example/a#2', { repo });
    expect(container.querySelectorAll('a a')).toHaveLength(0);
    expect(hrefs(container)).toEqual(['https://x.example', 'mailto:a@b.com', 'https://x.example/a#2']);
  });
});

describe('Markdown: allowlist HTML', () => {
  it('ammette details, summary, sub, sup, kbd, br, img; rimuove script, iframe, style, on*', () => {
    const { container } = renderMd(
      '<details><summary>S</summary>x<sub>1</sub><sup>2</sup><kbd>K</kbd><br><img src="https://e.example/i.png" onerror="alert(1)"></details>\n\n' +
        '<script>alert(1)</script><iframe src="https://e.example"></iframe><style>a{}</style><p onclick="x()">p</p>',
    );
    for (const t of ['details', 'summary', 'sub', 'sup', 'kbd', 'br', 'img']) {
      expect(container.querySelector(t)).not.toBeNull();
    }
    expect(container.querySelector('script, iframe, style')).toBeNull();
    expect(container.innerHTML).not.toMatch(/\son\w+=/i);
    expect(container.querySelector('img')).toHaveAttribute('referrerpolicy', 'no-referrer');
  });

  it('link esterni: _blank, noopener noreferrer, no-referrer', () => {
    const { container } = renderMd('[e](https://example.com)');
    const a = container.querySelector('a');
    expect(a).toHaveAttribute('target', '_blank');
    expect(a).toHaveAttribute('rel', 'noopener noreferrer');
    expect(a).toHaveAttribute('referrerpolicy', 'no-referrer');
  });

  it('mermaid resta un blocco di codice', () => {
    const { container } = renderMd('```mermaid\ngraph TD; A-->B\n```');
    expect(container.querySelector('pre code.language-mermaid')).not.toBeNull();
  });
});
