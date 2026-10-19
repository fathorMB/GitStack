import { render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { CommitDetail, CommitSummary } from '../../lib/codeApi';
import type { CombinedCommitStatus } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';

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
vi.mock('../../lib/pullsApi', async (orig) => ({ ...(await orig<typeof import('../../lib/pullsApi')>()), fetchCommitStatus: vi.fn(), fetchCommitStatuses: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import { fetchBranches, fetchCommit, fetchCommits, fetchTags } from '../../lib/codeApi';
import { fetchCommitStatus, fetchCommitStatuses } from '../../lib/pullsApi';
import { fetchRepo } from '../../lib/reposApi';

const SHA = 'a'.repeat(40);

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

const person = { name: 'Build', email: 'b@x', date: '2026-09-27T10:00:00Z', user: null };
const commit = (over: Partial<CommitSummary> = {}): CommitSummary => ({ sha: SHA, subject: 'feat: checks', author: person, committer: person, parents: [], ...over });

const status = (over: Partial<CombinedCommitStatus> = {}): CombinedCommitStatus => ({
  sha: SHA,
  state: 'failure',
  totalCount: 2,
  requiredContexts: [{ context: 'test', state: 'failure', satisfied: false }],
  statuses: [
    {
      id: 's1',
      sha: SHA,
      state: 'failure',
      context: 'test',
      description: '3 tests failed',
      targetUrl: 'https://ci.acme.local/builds/42',
      creator: { id: 'u', username: 'ci', kind: 'agent', avatarUrl: null } as never,
      createdAt: '2026-09-27T10:05:00Z',
    },
    {
      id: 's2',
      sha: SHA,
      state: 'success',
      context: 'lint',
      targetUrl: 'javascript:alert(1)',
      creator: { id: 'u', username: 'ci', kind: 'agent', avatarUrl: null } as never,
      createdAt: '2026-09-27T10:06:00Z',
    },
  ],
  ...over,
});

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/commits" element={<RepoPage mode="commits" />} />
        <Route path="/:owner/:repo/commit/:sha" element={<RepoPage mode="commit" />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'nobody', isAdmin: false } } as never);
  vi.mocked(fetchRepo).mockResolvedValue(repo());
  vi.mocked(fetchBranches).mockResolvedValue({ items: [{ name: 'main', isDefault: true, commit: commit() }], total: 1 } as never);
  vi.mocked(fetchTags).mockResolvedValue({ items: [], total: 0 });
});

describe('stati dei commit (PR9) nella vista commit e nella lista', () => {
  it('vista commit: sintesi, un riga per contesto e link al sistema esterno solo http(s)', async () => {
    const detail: CommitDetail = { commit: commit(), files: [], filesChanged: 0, additions: 0, deletions: 0, truncated: false, tags: [] } as CommitDetail;
    vi.mocked(fetchCommit).mockResolvedValue(detail);
    vi.mocked(fetchCommitStatus).mockResolvedValue(status());
    renderAt(`/acme/api/commit/${SHA}`);

    const checks = await screen.findByRole('region', { name: 'Commit statuses' });
    // Sintesi nell'intestazione e stato per contesto in ogni riga.
    expect(within(checks).getAllByText('Failure')).toHaveLength(2);
    expect(within(checks).getByText('Success')).toBeInTheDocument();
    expect(within(checks).getByText('test')).toBeInTheDocument();
    expect(within(checks).getByText('required')).toBeInTheDocument();
    expect(within(checks).getByText('3 tests failed')).toBeInTheDocument();
    const details = within(checks).getByRole('link', { name: /Details/ });
    expect(details).toHaveAttribute('href', 'https://ci.acme.local/builds/42');
    expect(details).toHaveAttribute('target', '_blank');
    expect(details).toHaveAttribute('rel', 'noopener noreferrer');
    // Il secondo contesto ha un URL non http(s): nessun link.
    expect(within(checks).getAllByRole('link', { name: /Details/ })).toHaveLength(1);
    expect(fetchCommitStatus).toHaveBeenCalledWith('acme', 'api', SHA);
  });

  it('vista commit: se gli stati non arrivano non c\'e\' nessun riquadro e nessun errore', async () => {
    const detail: CommitDetail = { commit: commit(), files: [], filesChanged: 0, additions: 0, deletions: 0, truncated: false, tags: [] } as CommitDetail;
    vi.mocked(fetchCommit).mockResolvedValue(detail);
    vi.mocked(fetchCommitStatus).mockRejectedValue(new Error('501'));
    renderAt(`/acme/api/commit/${SHA}`);
    expect(await screen.findByRole('heading', { name: 'feat: checks' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Commit statuses' })).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('lista dei commit: una sola richiesta per pagina e il badge giusto su ogni riga', async () => {
    const SHA2 = 'b'.repeat(40);
    const SHA3 = 'c'.repeat(40);
    vi.mocked(fetchCommits).mockResolvedValue({
      items: [commit(), commit({ sha: SHA2, subject: 'fix: second' }), commit({ sha: SHA3, subject: 'chore: third' })],
      page: 1,
      perPage: 30,
      hasMore: false,
    });
    vi.mocked(fetchCommitStatuses).mockResolvedValue({ [SHA]: 'success', [SHA2]: 'failure', [SHA3]: 'none' });
    renderAt('/acme/api/commits');
    const rowOf = async (subject: string) => (await screen.findByRole('link', { name: subject })).closest('.list-row') as HTMLElement;
    expect(await within(await rowOf('feat: checks')).findByText('Success')).toBeInTheDocument();
    expect(await within(await rowOf('fix: second')).findByText('Failure')).toBeInTheDocument();
    expect(within(await rowOf('chore: third')).queryByText(/Success|Failure|Pending|Error/)).toBeNull();
    expect(fetchCommitStatuses).toHaveBeenCalledTimes(1);
    expect(fetchCommitStatuses).toHaveBeenCalledWith('acme', 'api', [SHA, SHA2, SHA3]);
    expect(fetchCommitStatus).not.toHaveBeenCalled();
  });

  it('lista dei commit: se il batch fallisce non c\'e\' badge e nessun errore', async () => {
    vi.mocked(fetchCommits).mockResolvedValue({ items: [commit()], page: 1, perPage: 30, hasMore: false });
    vi.mocked(fetchCommitStatuses).mockRejectedValue(new Error('501'));
    renderAt('/acme/api/commits');
    const link = await screen.findByRole('link', { name: 'feat: checks' });
    const row = link.closest('.list-row') as HTMLElement;
    await vi.waitFor(() => expect(fetchCommitStatuses).toHaveBeenCalled());
    expect(within(row).queryByText(/Success|Failure|Pending|Error/)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
