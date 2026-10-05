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
    expect(container.querySelector('h2')).toHaveAttribute('id', 'titolo-uno');
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
    expect(href('d')).toBe('#top');
    const srcs = Array.from(container.querySelectorAll('img')).map((i) => i.getAttribute('src'));
    expect(srcs).toEqual(['/raw/o/r/main/docs/guide/img/p.png', '/raw/o/r/main/img/q.png', 'https://e.com/i.png']);
  });
});
