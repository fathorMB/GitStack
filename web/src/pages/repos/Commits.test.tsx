import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../lib/http';
import type { CommitDetail, CommitSummary, FileDiff } from '../../lib/codeApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';
import { groupByDay } from '../../lib/commitView';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/codeApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/codeApi')>()),
  fetchCommits: vi.fn(),
  fetchCommit: vi.fn(),
  fetchBranches: vi.fn(),
  fetchTags: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchBranches, fetchCommit, fetchCommits, fetchTags } from '../../lib/codeApi';
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

function commit(sha: string, subject: string, opts: { kind?: 'human' | 'agent'; date?: string; message?: string; parents?: string[] } = {}): CommitSummary {
  const kind = opts.kind ?? 'human';
  const person = {
    name: 'Build',
    email: 'b@x',
    date: opts.date ?? '2026-09-27T10:00:00Z',
    user: { id: 'u', username: kind === 'agent' ? 'build-agent' : 'lbianchi', kind, avatarUrl: null },
  };
  return { sha: sha.padEnd(40, '0'), subject, message: opts.message, author: person, committer: person, parents: opts.parents ?? [] };
}

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search}</div>;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/commits" element={<RepoPage mode="commits" />} />
        <Route path="/:owner/:repo/commits/*" element={<RepoPage mode="commits" />} />
        <Route path="/:owner/:repo/commit/:sha" element={<RepoPage mode="commit" />} />
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

describe('storico dei commit (mockup 09)', () => {
  const list = [
    commit('4e1a9c0', 'fix(ssh): accept ed25519 host keys', { kind: 'agent', date: '2026-09-27T12:00:00Z' }),
    commit('c81f3e2', 'test(ssh): cover ed25519', { kind: 'agent', date: '2026-09-27T09:00:00Z' }),
    commit('1d0e8aa', 'feat: structured logging', { date: '2026-09-25T09:00:00Z' }),
  ];

  it('groupByDay raggruppa i giorni consecutivi', () => {
    const g = groupByDay(list);
    expect(g.map((x) => [x.day, x.commits.length])).toEqual([
      ['Sep 27, 2026', 2],
      ['Sep 25, 2026', 1],
    ]);
  });

  it('raggruppa per giorno, mostra badge agent, tag e sha', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 1, perPage: 30, hasMore: false });
    vi.mocked(fetchTags).mockResolvedValue({ items: [{ name: 'v0.4.2', annotated: false, taggedAt: '2026-09-25T09:00:00Z', commit: list[2]! }], total: 1 });
    renderAt('/acme/api/commits');
    const day1 = await screen.findByRole('region', { name: 'Commits on Sep 27, 2026' });
    const day2 = screen.getByRole('region', { name: 'Commits on Sep 25, 2026' });
    expect(within(day1).getAllByText('agent')).toHaveLength(2);
    expect(within(day1).getByText('fix(ssh): accept ed25519 host keys')).toBeInTheDocument();
    expect(within(day2).queryByText('agent')).toBeNull();
    expect(within(day2).getByText('v0.4.2')).toBeInTheDocument();
    expect(within(day1).getByRole('link', { name: '4e1a9c0' })).toHaveAttribute('href', '/acme/api/commit/' + '4e1a9c0'.padEnd(40, '0'));
    expect(fetchCommits).toHaveBeenCalledWith('acme', 'api', { ref: 'main', path: '', author: '', page: 1 });
  });

  it('sha copiabile', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 1, perPage: 30, hasMore: false });
    renderAt('/acme/api/commits');
    await userEvent.click(await screen.findByRole('button', { name: 'Copy sha 4e1a9c0' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('4e1a9c0'.padEnd(40, '0')));
  });

  it('filtro per autore: richiede la lista filtrata e torna a pagina 1', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 1, perPage: 30, hasMore: false });
    renderAt('/acme/api/commits?page=2');
    await screen.findByRole('region', { name: 'Commits on Sep 27, 2026' });
    await userEvent.type(screen.getByLabelText('Filter by author'), 'build-agent');
    await userEvent.click(screen.getByRole('button', { name: 'Filter' }));
    await waitFor(() => expect(fetchCommits).toHaveBeenLastCalledWith('acme', 'api', { ref: 'main', path: '', author: 'build-agent', page: 1 }));
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api/commits?author=build-agent');
    await userEvent.click(await screen.findByRole('button', { name: 'Clear filter' }));
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/acme\/api\/commits$/));
  });

  it('paginazione Newer/Older', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 1, perPage: 30, hasMore: true });
    renderAt('/acme/api/commits');
    await screen.findByRole('region', { name: 'Commits on Sep 27, 2026' });
    expect(screen.getByRole('button', { name: 'Newer' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: 'Older' }));
    await waitFor(() => expect(fetchCommits).toHaveBeenLastCalledWith('acme', 'api', { ref: 'main', path: '', author: '', page: 2 }));
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api/commits?page=2');
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 2, perPage: 30, hasMore: false });
    await userEvent.click(await screen.findByRole('button', { name: 'Newer' }));
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/acme\/api\/commits$/));
  });

  it('History di un file: ref con slash e percorso nella richiesta', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: list, page: 1, perPage: 30, hasMore: false });
    renderAt('/acme/api/commits/feature/x/cmd/main.go');
    expect(await screen.findByLabelText('History of')).toHaveTextContent('cmd/main.go');
    await waitFor(() =>
      expect(fetchCommits).toHaveBeenCalledWith('acme', 'api', { ref: 'feature/x', path: 'cmd/main.go', author: '', page: 1 }),
    );
    expect(screen.getByRole('link', { name: 'Show all commits' })).toHaveAttribute('href', '/acme/api/commits/feature/x');
  });

  it('nessun commit', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: [], page: 1, perPage: 30, hasMore: false });
    renderAt('/acme/api/commits');
    expect(await screen.findByText('No commits.')).toBeInTheDocument();
  });
});

describe('dettaglio commit (mockup 10)', () => {
  const sha = '4e1a9c0'.padEnd(40, '0');
  const f = (over: Partial<FileDiff>): FileDiff => ({
    path: 'internal/ssh/server.go',
    status: 'modified',
    additions: 1,
    deletions: 1,
    binary: false,
    truncated: false,
    patch: '@@ -1,2 +1,2 @@\n ctx\n-before\n+after\n',
    ...over,
  });
  const detail = (over: Partial<CommitDetail> = {}): CommitDetail => ({
    commit: commit('4e1a9c0', 'fix(ssh): accept ed25519 host keys', { kind: 'agent', message: 'fix(ssh): accept ed25519 host keys\n\nThe server only advertised RSA.', parents: ['c81f3e2'.padEnd(40, '0')] }),
    files: [f({}), f({ path: 'go.sum', collapsed: true, collapseReason: 'lock' })],
    filesChanged: 2,
    additions: 2,
    deletions: 2,
    truncated: false,
    tags: ['v0.4.2'],
    ...over,
  });

  it('messaggio, autore agent, genitori, tag e conteggi', async () => {
    vi.mocked(fetchCommit).mockResolvedValue(detail());
    renderAt(`/acme/api/commit/${sha}`);
    expect(await screen.findByRole('heading', { name: 'fix(ssh): accept ed25519 host keys' })).toBeInTheDocument();
    expect(screen.getByLabelText('Commit message')).toHaveTextContent('The server only advertised RSA.');
    expect(screen.getByText('agent')).toBeInTheDocument();
    expect(screen.getByLabelText('Parents')).toHaveTextContent('c81f3e2');
    expect(screen.getByRole('link', { name: 'c81f3e2' })).toHaveAttribute('href', '/acme/api/commit/' + 'c81f3e2'.padEnd(40, '0'));
    expect(screen.getByLabelText('Tags')).toHaveTextContent('v0.4.2');
    expect(screen.getByText(/Showing/)).toHaveTextContent('Showing 2 changed files with 2 additions and 2 deletions');
    expect(fetchCommit).toHaveBeenCalledWith('acme', 'api', sha, false);
    expect(screen.getAllByRole('link', { name: 'View file' })[0]).toHaveAttribute('href', `/acme/api/blob/${sha}/internal/ssh/server.go`);
    expect(screen.queryByText(/closed #/)).toBeNull();
  });

  it('autore e committer diversi', async () => {
    const d = detail();
    d.commit.committer = { name: 'Mario', email: 'm@x', date: '2026-09-27T11:00:00Z', user: null };
    vi.mocked(fetchCommit).mockResolvedValue(d);
    renderAt(`/acme/api/commit/${sha}`);
    expect(await screen.findByLabelText('Committer')).toHaveTextContent('committed by Mario');
  });

  it('file chiuso con motivo e Load diff', async () => {
    vi.mocked(fetchCommit).mockResolvedValue(detail());
    renderAt(`/acme/api/commit/${sha}`);
    await screen.findByRole('table', { name: 'Unified diff of internal/ssh/server.go' });
    expect(screen.getByText('Lock file')).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'Unified diff of go.sum' })).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'Load diff' }));
    expect(screen.getByRole('table', { name: 'Unified diff of go.sum' })).toBeInTheDocument();
  });

  it('Unified e Split', async () => {
    vi.mocked(fetchCommit).mockResolvedValue(detail());
    renderAt(`/acme/api/commit/${sha}`);
    await screen.findByRole('table', { name: 'Unified diff of internal/ssh/server.go' });
    expect(screen.getByRole('button', { name: 'Unified' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: 'Split' }));
    expect(screen.getByRole('table', { name: 'Split diff of internal/ssh/server.go' })).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'Unified diff of internal/ssh/server.go' })).toBeNull();
  });

  it('ignora spazi: ricarica con ignoreWhitespace e aggiorna i download', async () => {
    vi.mocked(fetchCommit).mockResolvedValue(detail());
    renderAt(`/acme/api/commit/${sha}`);
    await screen.findByRole('table', { name: 'Unified diff of internal/ssh/server.go' });
    expect(screen.getByRole('link', { name: '.patch' })).toHaveAttribute('href', expect.stringMatching(new RegExp(`/commits/${sha}/patch\\?format=patch$`)));
    await userEvent.click(screen.getByRole('checkbox', { name: 'Ignore whitespace' }));
    await waitFor(() => expect(fetchCommit).toHaveBeenLastCalledWith('acme', 'api', sha, true));
    expect(await screen.findByRole('link', { name: '.diff' })).toHaveAttribute('href', expect.stringMatching(/format=diff&ignoreWhitespace=true$/));
  });

  it('oltre i limiti: solo elenco, avviso e download', async () => {
    vi.mocked(fetchCommit).mockResolvedValue(
      detail({
        listOnly: true,
        truncated: true,
        filesChanged: 450,
        files: [f({ patch: undefined }), f({ path: 'b.go', patch: undefined, collapsed: true, collapseReason: 'large' })],
      }),
    );
    renderAt(`/acme/api/commit/${sha}`);
    expect(await screen.findByRole('status')).toHaveTextContent('only the list of files is shown, the first 2 of 450');
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Load diff' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Split' })).toBeNull();
    expect(screen.getAllByRole('link', { name: '.diff' }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('link', { name: '.patch' }).length).toBeGreaterThan(0);
    expect(screen.getByText('internal/ssh/server.go')).toBeInTheDocument();
    expect(screen.getByText('b.go')).toBeInTheDocument();
  });

  it('errore del caricamento', async () => {
    vi.mocked(fetchCommit).mockRejectedValue(new ApiError({ error: { code: 'not_found', message: 'Commit not found' } }, 404));
    renderAt(`/acme/api/commit/${sha}`);
    expect(await screen.findByText(/Commit not found/)).toBeInTheDocument();
  });
});
