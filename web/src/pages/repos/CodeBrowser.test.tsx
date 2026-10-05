import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Link, MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../lib/http';
import { fuzzyFilter, splitRefPath } from '../../lib/codeApi';
import type { CommitSummary, Tree, TreeEntry } from '../../lib/codeApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/codeApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/codeApi')>()),
  fetchTree: vi.fn(),
  fetchReadme: vi.fn(),
  fetchBranches: vi.fn(),
  fetchTags: vi.fn(),
  fetchLanguages: vi.fn(),
  fetchFilePaths: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchBranches, fetchFilePaths, fetchLanguages, fetchReadme, fetchTags, fetchTree } from '../../lib/codeApi';
import { fetchRepo } from '../../lib/reposApi';

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api-gateway',
    fullName: 'acme/api-gateway',
    description: 'Single entry point',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: false,
    cloneUrls: { https: 'https://git.acme.local/acme/api-gateway.git', ssh: 'ssh://git@git.acme.local:2222/acme/api-gateway.git' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
    ...over,
  };
}

function commit(subject: string, kind: 'human' | 'agent' = 'human'): CommitSummary {
  const person = { name: 'build', email: 'b@x', date: '2026-10-01T00:00:00Z', user: { id: 'u', username: 'build-agent', kind, avatarUrl: null } };
  return { sha: 'abcdef1234567', subject, author: person, committer: person, parents: [] };
}

function entry(path: string, type: TreeEntry['type'] = 'file'): TreeEntry {
  return { name: path.split('/').pop() ?? path, path, type, mode: type === 'dir' ? '040000' : '100644', lastCommit: commit(`msg ${path}`) };
}

function tree(ref: string, path: string, entries: TreeEntry[]): Tree {
  return { ref, commitSha: 'abcdef1234567', path, entries, truncated: false };
}

function branch(name: string, isDefault = false) {
  return { name, isDefault, protected: false, commit: commit(`head of ${name}`, 'agent') };
}

function file(name: string, content: string, path = name) {
  return { ref: 'main', path, name, sha: 's', size: content.length, binary: false, kind: 'text' as const, display: 'highlight' as const, truncated: false, encoding: 'utf-8' as const, content, lastCommit: commit('r') };
}

function Where() {
  const l = useLocation();
  return <output data-testid="where">{l.pathname + l.search}</output>;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo" element={<RepoPage />} />
        <Route path="/:owner/:repo/tree/*" element={<RepoPage />} />
        <Route path="/:owner/:repo/search" element={<Where />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'nobody', isAdmin: false } } as never);
  vi.mocked(fetchRepo).mockResolvedValue(repo());
  vi.mocked(fetchBranches).mockResolvedValue({ items: [branch('main', true), branch('dev'), branch('feature/x')], total: 3 });
  vi.mocked(fetchTags).mockResolvedValue({
    items: [
      { name: 'v0.4.2', annotated: false, taggedAt: '2026-10-02T00:00:00Z', commit: commit('t') },
      { name: 'v0.1.0', annotated: false, taggedAt: '2026-09-01T00:00:00Z', commit: commit('t') },
    ],
    total: 2,
  });
  vi.mocked(fetchLanguages).mockResolvedValue({ languages: [{ name: 'Go', bytes: 90, percent: 90 }, { name: 'Weird', bytes: 10, percent: 10 }], totalBytes: 100 });
  vi.mocked(fetchReadme).mockResolvedValue(null);
  vi.mocked(fetchTree).mockImplementation(async (_o, _r, ref, path) =>
    path === 'cmd'
      ? tree(ref, path, [entry('cmd/gateway', 'dir'), entry('cmd/main.go')])
      : tree(ref, '', [entry('README.md'), entry('LICENSE'), entry('cmd', 'dir'), entry('internal', 'dir')]),
  );
});

describe('CodeBrowser', () => {
  it('mostra albero, ultimo commit, conteggi, About e niente Star/Watch, con la scheda Issues (M-05)', async () => {
    renderAt('/acme/api-gateway');
    const files = await screen.findByLabelText('Files');
    const rows = within(files).getAllByRole('link').map((a) => a.textContent);
    expect(rows).toEqual(['cmd', 'internal', 'LICENSE', 'README.md']); // cartelle prima
    expect(within(files).getByLabelText('Latest commit')).toHaveTextContent('head of main');
    expect(within(files).getByText('agent')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /2 tags/ })).toHaveAttribute('href', '/acme/api-gateway/tags');
    expect(screen.getByText(/3/, { selector: 'b' })).toBeInTheDocument();
    const about = screen.getByLabelText('About');
    expect(within(about).getByText('Single entry point')).toBeInTheDocument();
    expect(await within(about).findByText('Go')).toBeInTheDocument();
    expect(within(about).getByRole('link', { name: 'v0.4.2' })).toBeInTheDocument();
    expect(within(about).getByText(/License file/)).toBeInTheDocument();
    expect(screen.queryByText(/star/i)).toBeNull();
    expect(screen.queryByText(/watch/i)).toBeNull();
    expect(screen.getByRole('link', { name: /Issues/ })).toHaveAttribute('href', '/acme/api-gateway/issues');
    expect(fetchTree).toHaveBeenCalledWith('acme', 'api-gateway', 'main', '');
  });

  it('naviga nelle cartelle con breadcrumb e ricarica', async () => {
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await user.click(await screen.findByRole('link', { name: 'cmd' }));
    expect(await screen.findByRole('link', { name: 'gateway' })).toBeInTheDocument();
    expect(fetchTree).toHaveBeenLastCalledWith('acme', 'api-gateway', 'main', 'cmd');
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api-gateway/tree/main/cmd');
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' });
    expect(within(crumbs).getByText('cmd')).toHaveAttribute('aria-current', 'page');
    await user.click(within(crumbs).getByRole('link', { name: 'api-gateway' }));
    expect(await screen.findByRole('link', { name: 'internal' })).toBeInTheDocument();
    expect(fetchTree).toHaveBeenLastCalledWith('acme', 'api-gateway', 'main', '');
  });

  it('cambia ref dal selettore (anche con barra nel nome) e ricarica', async () => {
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: /Switch branch or tag/ }));
    await user.type(screen.getByLabelText('Find a branch or tag'), 'feat');
    expect(screen.queryByRole('link', { name: /dev/ })).toBeNull();
    await user.click(screen.getByRole('link', { name: 'feature/x' }));
    await waitFor(() => expect(fetchTree).toHaveBeenLastCalledWith('acme', 'api-gateway', 'feature/x', ''));
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api-gateway/tree/feature/x');
    expect(screen.getByRole('button', { name: /current: feature\/x/ })).toBeInTheDocument();
  });

  it('ricarica quando la rotta passa da un repo a un altro', async () => {
    vi.mocked(fetchRepo).mockImplementation(async (o, r) => repo({ owner: { type: 'organization', name: o }, name: r, fullName: `${o}/${r}`, description: `desc ${o}/${r}` }));
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/a/x']}>
        <Link to="/b/y">go</Link>
        <Routes>
          <Route path="/:owner/:repo" element={<RepoPage />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(await screen.findByRole('heading', { name: 'a / x' })).toBeInTheDocument();
    await user.click(screen.getByRole('link', { name: 'go' }));
    expect(await screen.findByRole('heading', { name: 'b / y' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'a / x' })).toBeNull();
    expect(fetchTree).toHaveBeenLastCalledWith('b', 'y', 'main', '');
  });

  it('README presente (con resolver sul raw) e assente', async () => {
    vi.mocked(fetchReadme).mockResolvedValue(file('README.md', '# Hello\n\n![logo](img/logo.png)\n\n[doc](docs/a.md)'));
    const { unmount } = renderAt('/acme/api-gateway');
    const card = await screen.findByLabelText('README');
    expect(within(card).getByRole('heading', { name: 'Hello' })).toBeInTheDocument();
    expect(within(card).getByRole('img', { name: 'logo' })).toHaveAttribute('src', '/api/repos/acme/api-gateway/raw/main/img/logo.png');
    expect(within(card).getByRole('link', { name: 'doc' })).toHaveAttribute('href', '/acme/api-gateway/blob/main/docs/a.md');
    unmount();
    vi.mocked(fetchReadme).mockResolvedValue(null);
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await waitFor(() => expect(fetchReadme).toHaveBeenCalledTimes(2));
    expect(screen.queryByLabelText('README')).toBeNull();
  });

  it('menu Clone: SSH con porta 2222 e con porta 22, CLI, download ZIP e tar.gz', async () => {
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: /Clone/ }));
    const menu = screen.getByRole('dialog', { name: 'Clone' });
    expect(within(menu).getByLabelText('Clone URL')).toHaveValue('https://git.acme.local/acme/api-gateway.git');
    await user.click(within(menu).getByRole('button', { name: 'SSH' }));
    expect(within(menu).getByText('SSH (port 2222)')).toBeInTheDocument();
    expect(within(menu).getByLabelText('Clone URL')).toHaveValue('ssh://git@git.acme.local:2222/acme/api-gateway.git');
    await user.click(within(menu).getByRole('button', { name: 'CLI' }));
    expect(within(menu).getByLabelText('Clone URL')).toHaveValue('gs repo clone acme/api-gateway');
    expect(within(menu).getByRole('link', { name: /Download ZIP/ })).toHaveAttribute('href', '/api/repos/acme/api-gateway/archive?ref=main&format=zip');
    expect(within(menu).getByRole('link', { name: /Download tar.gz/ })).toHaveAttribute('href', '/api/repos/acme/api-gateway/archive?ref=main&format=tar.gz');
  });

  it('menu Clone con porta 22 usa la forma corta', async () => {
    vi.mocked(fetchRepo).mockResolvedValue(
      repo({ cloneUrls: { https: 'https://git.acme.local/acme/api-gateway.git', ssh: 'ssh://git@git.acme.local/acme/api-gateway.git', sshShort: 'git@git.acme.local:acme/api-gateway.git' } }),
    );
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: /Clone/ }));
    await user.click(screen.getByRole('button', { name: 'SSH' }));
    expect(screen.getByText('SSH (port 22)')).toBeInTheDocument();
    expect(screen.getByLabelText('Clone URL')).toHaveValue('git@git.acme.local:acme/api-gateway.git');
  });

  it('menu Clone senza cloneUrls.ssh (SSH spento): niente scheda SSH', async () => {
    vi.mocked(fetchRepo).mockResolvedValue(repo({ cloneUrls: { https: 'https://git.acme.local/acme/api-gateway.git' } }));
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: /Clone/ }));
    expect(screen.queryByRole('button', { name: 'SSH' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Clone URL')).toHaveValue('https://git.acme.local/acme/api-gateway.git');
    expect(screen.queryByText(/SSH \(port/)).not.toBeInTheDocument();
  });

  it('Go to file: corrispondenza approssimata', async () => {
    vi.mocked(fetchFilePaths).mockResolvedValue({ ref: 'main', commitSha: 's', truncated: false, paths: ['README.md', 'cmd/gateway/main.go', 'internal/ssh/hostkeys.go', 'go.mod'] });
    const user = userEvent.setup();
    renderAt('/acme/api-gateway');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: 'Go to file' }));
    await user.type(await screen.findByLabelText('File path'), 'cgmain');
    const list = await screen.findByRole('list', { name: 'Matching files' });
    expect(within(list).getAllByRole('link').map((a) => a.textContent)).toEqual(['cmd/gateway/main.go']);
    expect(within(list).getByRole('link')).toHaveAttribute('href', '/acme/api-gateway/blob/main/cmd/gateway/main.go');
  });

  it('Search code porta ai risultati con query e ref', async () => {
    const user = userEvent.setup();
    renderAt('/acme/api-gateway/tree/dev');
    await screen.findByLabelText('Files');
    await user.click(screen.getByRole('button', { name: 'Search code' }));
    await user.type(screen.getByLabelText('Search query'), 'func main{enter}');
    await waitFor(() => expect(screen.getAllByTestId('where')[0]).toHaveTextContent('/acme/api-gateway/search?q=func%20main&ref=dev'));
  });

  it('mostra l\'errore del caricamento', async () => {
    vi.mocked(fetchTree).mockRejectedValue(new ApiError({ error: { code: 'ref_not_found', message: 'Ref not found.' } }, 404));
    renderAt('/acme/api-gateway/tree/nope');
    expect(await screen.findByText('Ref not found.')).toBeInTheDocument();
  });
});

describe('helper', () => {
  it('splitRefPath prende il prefisso piu\' lungo', () => {
    expect(splitRefPath('feature/x/cmd/a', ['feature', 'feature/x'])).toEqual({ ref: 'feature/x', path: 'cmd/a' });
    expect(splitRefPath('abc1234/src', ['main'])).toEqual({ ref: 'abc1234', path: 'src' });
    expect(splitRefPath('main', ['main'])).toEqual({ ref: 'main', path: '' });
  });
  it('fuzzyFilter ordina per pertinenza', () => {
    const paths = ['docs/gateway-notes.md', 'cmd/gateway/main.go', 'main.go'];
    expect(fuzzyFilter('main', paths)[0]).toBe('main.go');
    expect(fuzzyFilter('xyz', paths)).toEqual([]);
  });
});

