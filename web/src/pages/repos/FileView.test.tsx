import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../lib/http';
import type { Blame, CommitSummary, FileContent } from '../../lib/codeApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';
import { parseLineHash, splitLines } from '../../lib/codeLines';
import { isMarkdownName } from '../../lib/codeApi';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/codeApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/codeApi')>()),
  fetchFile: vi.fn(),
  fetchBlame: vi.fn(),
  fetchBranches: vi.fn(),
  fetchTags: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchBlame, fetchBranches, fetchFile, fetchTags } from '../../lib/codeApi';
import { fetchRepo } from '../../lib/reposApi';

const repo = (): Repository =>
  ({
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api',
    fullName: 'acme/api',
    description: '',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: false,
    cloneUrls: { https: 'h', ssh: 's' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
  }) as Repository;

function commit(sha: string, subject: string, kind: 'human' | 'agent' = 'human'): CommitSummary {
  const person = {
    name: 'Build',
    email: 'b@x',
    date: '2026-10-01T00:00:00Z',
    user: { id: 'u', username: kind === 'agent' ? 'build-agent' : 'lbianchi', kind, avatarUrl: null },
  };
  return { sha: sha.padEnd(40, '0'), subject, author: person, committer: person, parents: [] };
}

function file(over: Partial<FileContent>): FileContent {
  return {
    ref: 'main',
    path: 'cmd/main.go',
    name: 'main.go',
    sha: 'b'.repeat(40),
    size: 100,
    binary: false,
    kind: 'text',
    display: 'highlight',
    truncated: false,
    encoding: 'utf-8',
    content: 'package main\n\nfunc main() {}\n',
    lastCommit: commit('9b27d11', 'feat: graceful shutdown'),
    ...over,
  };
}

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.hash}</div>;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/blob/*" element={<RepoPage mode="blob" />} />
        <Route path="/:owner/:repo/blame/*" element={<RepoPage mode="blame" />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

const branch = (name: string) => ({ name, isDefault: name === 'main', commit: commit('c', 'x') });

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'nobody', isAdmin: false } } as never);
  vi.mocked(fetchRepo).mockResolvedValue(repo());
  vi.mocked(fetchBranches).mockResolvedValue({ items: [branch('main'), branch('feature/x')], total: 2 } as never);
  vi.mocked(fetchTags).mockResolvedValue({ items: [], total: 0 });
});

describe('FileView', () => {
  it('testo sotto 1 MB: righe numerate, intestazione, link History e Raw', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({}));
    renderAt('/acme/api/blob/main/cmd/main.go');
    expect(await screen.findByLabelText('File info')).toHaveTextContent('3 lines · 100 B');
    expect(fetchFile).toHaveBeenCalledWith('acme', 'api', 'main', 'cmd/main.go');
    expect(screen.getByLabelText('Latest commit')).toHaveTextContent('feat: graceful shutdown');
    expect(screen.getByRole('link', { name: 'Line 3' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /History/ })).toHaveAttribute('href', '/acme/api/commits/main/cmd/main.go');
    expect(screen.getByRole('link', { name: 'Raw' })).toHaveAttribute('href', '/acme/api/raw/main/cmd/main.go');
    expect(screen.getByRole('link', { name: 'Blame' })).toHaveAttribute('href', '/acme/api/blame/main/cmd/main.go');
    expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('download', 'main.go');
    // evidenziazione caricata in modo asincrono
    await waitFor(() => expect(document.querySelector('.hljs-keyword')).not.toBeNull(), { timeout: 20000 });
    expect(document.querySelectorAll('.codeline')).toHaveLength(3);
  }, 30000);

  it('ref con slash: feature/x/cmd/main.go', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({}));
    renderAt('/acme/api/blob/feature/x/cmd/main.go');
    await screen.findByLabelText('File info');
    expect(fetchFile).toHaveBeenCalledWith('acme', 'api', 'feature/x', 'cmd/main.go');
  });

  it('testo fra 1 e 5 MB: semplice, senza evidenziazione', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({ display: 'plain', size: 2 * 1048576 }));
    renderAt('/acme/api/blob/main/cmd/main.go');
    expect(await screen.findByLabelText('File info')).toHaveTextContent('2.0 MB');
    await new Promise((r) => setTimeout(r, 50));
    expect(document.querySelector('.hljs-keyword')).toBeNull();
    expect(document.querySelector('.codelines')).toHaveAttribute('data-highlight', 'false');
    expect(screen.getByText('package main')).toBeInTheDocument();
  });

  it('testo oltre 5 MB: messaggio e Download, niente righe', async () => {
    vi.mocked(fetchFile).mockResolvedValue(
      file({ display: 'download', size: 6 * 1048576, truncated: true, content: undefined, encoding: undefined }),
    );
    renderAt('/acme/api/blob/main/cmd/main.go');
    expect(await screen.findByText('File too large to display')).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: /Download/ })[0]).toBeTruthy();
    expect(document.querySelector('.codeline')).toBeNull();
  });

  it('Markdown: anteprima renderizzata e passaggio al sorgente', async () => {
    const user = userEvent.setup();
    vi.mocked(fetchFile).mockResolvedValue(
      file({ path: 'docs/guide.md', name: 'guide.md', content: '# Titolo\n\ntesto **forte**\n' }),
    );
    renderAt('/acme/api/blob/main/docs/guide.md');
    expect(await screen.findByRole('heading', { name: 'Titolo' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Source' }));
    expect(screen.queryByRole('heading', { name: 'Titolo' })).toBeNull();
    expect(screen.getByText('# Titolo')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Preview' }));
    expect(await screen.findByRole('heading', { name: 'Titolo' })).toBeInTheDocument();
  });

  it('build.cmd non e Markdown: righe di codice e nessun pulsante Source', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({ path: 'build.cmd', name: 'build.cmd', content: '@echo off\n' }));
    renderAt('/acme/api/blob/main/build.cmd');
    await screen.findByLabelText('File info');
    expect(document.querySelectorAll('.codeline')).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Source' })).toBeNull();
  });

  it('immagine: solo <img> con data URL', async () => {
    vi.mocked(fetchFile).mockResolvedValue(
      file({ path: 'logo.png', name: 'logo.png', kind: 'image', binary: true, display: 'image', mimeType: 'image/png', encoding: 'base64', content: 'iVBORw0KGgo=' }),
    );
    renderAt('/acme/api/blob/main/logo.png');
    const img = await screen.findByRole('img', { name: 'logo.png' });
    expect(img).toHaveAttribute('src', 'data:image/png;base64,iVBORw0KGgo=');
  });

  it('SVG: mostrato solo come <img>, il markup non entra nella pagina', async () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><circle r="5"/></svg>';
    vi.mocked(fetchFile).mockResolvedValue(
      file({ path: 'a.svg', name: 'a.svg', kind: 'image', binary: true, display: 'image', mimeType: 'image/svg+xml', encoding: 'base64', content: btoa(svg) }),
    );
    renderAt('/acme/api/blob/main/a.svg');
    const img = await screen.findByRole('img', { name: 'a.svg' });
    expect(img.tagName).toBe('IMG');
    expect(img.getAttribute('src')).toMatch(/^data:image\/svg\+xml;base64,/);
    expect(document.querySelector('svg:not([class*=lucide]) circle')).toBeNull();
    expect(document.querySelector('script')).toBeNull();
    expect(screen.queryByText(/alert/)).toBeNull();
  });

  it('binario: dimensione e Download', async () => {
    vi.mocked(fetchFile).mockResolvedValue(
      file({ path: 'bin/tool', name: 'tool', kind: 'binary', binary: true, display: 'download', size: 4096, content: undefined, encoding: undefined }),
    );
    renderAt('/acme/api/blob/main/bin/tool');
    expect(await screen.findByText('Binary file')).toBeInTheDocument();
    expect(screen.getAllByText('4.0 KB').length).toBeGreaterThan(0);
    expect(document.querySelector('.codeline')).toBeNull();
  });

  it('ancora #L2-L3 evidenzia le righe; il click e shift+click aggiornano il link', async () => {
    const user = userEvent.setup();
    vi.mocked(fetchFile).mockResolvedValue(file({ content: 'a\nb\nc\nd\n' }));
    renderAt('/acme/api/blob/main/cmd/main.go#L2-L3');
    await screen.findByLabelText('Line 1');
    const selected = () => Array.from(document.querySelectorAll('.codeline[data-selected]')).map((e) => e.querySelector('.lno')?.textContent);
    expect(selected()).toEqual(['2', '3']);
    await user.click(screen.getByLabelText('Line 4'));
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api/blob/main/cmd/main.go#L4');
    expect(selected()).toEqual(['4']);
    await user.keyboard('{Shift>}');
    await user.click(screen.getByLabelText('Line 2'));
    await user.keyboard('{/Shift}');
    expect(screen.getByTestId('where')).toHaveTextContent('#L2-L4');
    expect(selected()).toEqual(['2', '3', '4']);
  });
});

describe('Blame', () => {
  const blame = (): Blame => ({
    ref: 'main',
    path: 'cmd/main.go',
    ranges: [
      { startLine: 1, endLine: 2, commit: commit('a1f0c3e', 'initial commit', 'agent') },
      { startLine: 3, endLine: 3, commit: commit('e5c2d90', 'feat: slog') },
    ],
  });

  it('mostra commit, autore con badge agent e data per ogni blocco', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({}));
    vi.mocked(fetchBlame).mockResolvedValue(blame());
    renderAt('/acme/api/blame/main/cmd/main.go');
    const first = await screen.findByLabelText('Lines 1-2');
    expect(within(first).getByText('build-agent')).toBeInTheDocument();
    expect(within(first).getByText('agent')).toBeInTheDocument();
    expect(within(first).getByRole('link', { name: /a1f0c3e initial commit/ })).toHaveAttribute(
      'href',
      `/acme/api/commit/${'a1f0c3e'.padEnd(40, '0')}`,
    );
    expect(within(first).getByText(/ago/)).toBeInTheDocument();
    const second = screen.getByLabelText('Lines 3-3');
    expect(within(second).getByText('lbianchi')).toBeInTheDocument();
    expect(within(second).queryByText('agent')).toBeNull();
    expect(screen.getByRole('link', { name: 'Blame' })).toHaveAttribute('aria-current', 'page');
    expect(fetchBlame).toHaveBeenCalledWith('acme', 'api', 'main', 'cmd/main.go');
  });

  it('oltre 1 MB non chiama il blame', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({ display: 'plain', size: 2 * 1048576 }));
    renderAt('/acme/api/blame/main/cmd/main.go');
    expect(await screen.findByText(/Blame is available for text files up to 1 MB/)).toBeInTheDocument();
    expect(fetchBlame).not.toHaveBeenCalled();
  });

  it('400 blame_unavailable dal servizio: messaggio, nessun errore', async () => {
    vi.mocked(fetchFile).mockResolvedValue(file({}));
    vi.mocked(fetchBlame).mockRejectedValue(new ApiError({ error: { code: 'blame_unavailable', message: 'no' } }, 400));
    renderAt('/acme/api/blame/main/cmd/main.go');
    expect(await screen.findByText('Blame is not available for this file.')).toBeInTheDocument();
  });
});

describe('utilita', () => {
  it('isMarkdownName riconosce solo .md e .markdown', () => {
    expect(isMarkdownName('README.md')).toBe(true);
    expect(isMarkdownName('x.MARKDOWN')).toBe(true);
    expect(isMarkdownName('build.cmd')).toBe(false);
    expect(isMarkdownName('cmd')).toBe(false);
    expect(isMarkdownName('notes.txt')).toBe(false);
  });
  it('parseLineHash', () => {
    expect(parseLineHash('#L10')).toEqual([10, 10]);
    expect(parseLineHash('#L10-L20')).toEqual([10, 20]);
    expect(parseLineHash('#L20-L10')).toEqual([10, 20]);
    expect(parseLineHash('#intro')).toBeNull();
  });
  it('splitLines spezza i token multi-riga', () => {
    expect(splitLines(['a\nb'])).toEqual([['a'], ['b']]);
  });
});
