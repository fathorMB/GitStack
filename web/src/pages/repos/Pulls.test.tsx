import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { PullRequest, PullRequestSummary } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { PullDetailPage } from './PullDetailPage';
import { PullsPage } from './PullsPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/issuesApi', () => ({ fetchMyRole: vi.fn() }));
vi.mock('../../lib/pullsApi', () => ({
  fetchPulls: vi.fn(),
  countPulls: vi.fn(),
  fetchPull: vi.fn(),
  fetchMergeability: vi.fn(),
  fetchCommitStatus: vi.fn(),
  fetchPullReviews: vi.fn(),
  fetchPullComments: vi.fn(),
  postPullComment: vi.fn(),
  markReady: vi.fn(),
  closePull: vi.fn(),
  reopenPull: vi.fn(),
  mergePull: vi.fn(),
  deletePullBranch: vi.fn(),
  requestReviewers: vi.fn(),
  dropReviewer: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchMyRole } from '../../lib/issuesApi';
import * as api from '../../lib/pullsApi';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', fullName: 'acme/api', defaultBranch: 'main', archived: false, empty: false } as Repository;
const agent = { id: 'b', username: 'build-agent', kind: 'agent' as const };
const person = { id: 'f', username: 'fneri', kind: 'human' as const };

const summary = (o: Partial<PullRequestSummary>): PullRequestSummary => ({
  number: 58,
  title: 'fix(webhooks): stop retrying',
  state: 'open',
  draft: false,
  author: agent,
  sourceBranch: 'agent/57',
  targetBranch: 'main',
  reviewDecision: 'approved',
  statusState: 'success',
  requestedReviewers: [],
  commentCount: 5,
  createdAt: '2026-10-18T10:00:00Z',
  updatedAt: '2026-10-18T10:00:00Z',
  ...o,
});

const full = (o: Partial<PullRequest> = {}): PullRequest => ({
  ...summary({}),
  id: 'p58',
  body: 'Fixes #57',
  headSha: 'abc1234',
  sourceBranchExists: true,
  approvals: 1,
  requiredApprovals: 1,
  unresolvedThreadCount: 0,
  closingIssues: [57],
  commitCount: 3,
  changedFiles: 2,
  additions: 4,
  deletions: 1,
  ...o,
});

beforeEach(() => {
  vi.clearAllMocks();
});

describe('lista delle Pull Request', () => {
  it('mostra bozze, review, stati dei commit e il filtro nell indirizzo', async () => {
    vi.mocked(api.countPulls).mockResolvedValue(4);
    vi.mocked(api.fetchPulls).mockResolvedValue({
      page: 1,
      perPage: 30,
      total: 3,
      items: [
        summary({}),
        summary({ number: 55, title: 'Rate limiter', author: person, reviewDecision: 'required', statusState: 'failure', commentCount: 2 }),
        summary({ number: 52, title: 'feat(api): paginate', draft: true, reviewDecision: 'none', statusState: 'pending', commentCount: 0 }),
      ],
    });
    render(
      <MemoryRouter initialEntries={['/acme/api/pulls']}>
        <PullsPage repo={repo} />
      </MemoryRouter>,
    );
    const row58 = await screen.findByTestId('pull-58');
    expect(row58).toHaveTextContent('✓ Approved');
    expect(row58).toHaveTextContent('✓ Checks passed');
    expect(row58).toHaveTextContent('agent');
    expect(screen.getByTestId('pull-55')).toHaveTextContent('Review required');
    expect(screen.getByTestId('pull-55')).toHaveTextContent('Checks failed');
    expect(screen.getByTestId('pull-52')).toHaveTextContent('Draft');
    expect(screen.getByTestId('pull-52')).toHaveTextContent('checks running');
    expect(screen.getByRole('link', { name: /New pull request/ })).toHaveAttribute('href', '/acme/api/pulls/new');
    expect(api.fetchPulls).toHaveBeenCalledWith('acme', 'api', 'is:open', '');
    await userEvent.setup().click(screen.getByRole('button', { name: /Merged/ }));
    await waitFor(() => expect(api.fetchPulls).toHaveBeenLastCalledWith('acme', 'api', 'is:merged', ''));
  }, 15000);
});

function detail(o: { role: 'read' | 'write'; pr?: PullRequest; me?: string }) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: o.me ?? 'lbianchi', isAdmin: false } } as never);
  vi.mocked(fetchMyRole).mockResolvedValue(o.role);
  vi.mocked(api.fetchPull).mockResolvedValue(o.pr ?? full());
  vi.mocked(api.fetchMergeability).mockResolvedValue({
    state: 'mergeable',
    mergeable: true,
    conflictFiles: [],
    behindBy: 0,
    aheadBy: 3,
    blockers: [],
    allowedMethods: ['squash', 'merge'],
    defaultMethod: 'squash',
    approvals: 1,
    requiredApprovals: 1,
    headSha: 'abc1234',
  });
  vi.mocked(api.fetchCommitStatus).mockResolvedValue({ sha: 'abc1234', state: 'success', statuses: [], totalCount: 0, requiredContexts: [] });
  vi.mocked(api.fetchPullReviews).mockResolvedValue([]);
  vi.mocked(api.fetchPullComments).mockResolvedValue([
    { id: 'c1', author: person, body: 'Looks good', outdated: false, edited: false, deleted: false, createdAt: '2026-10-18T11:00:00Z', updatedAt: '2026-10-18T11:00:00Z' },
  ]);
  render(
    <MemoryRouter initialEntries={['/acme/api/pulls/58']}>
      <Routes>
        <Route path="/:owner/:repo/pulls/:number" element={<PullDetailPage repo={repo} />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('Conversation', () => {
  it('con write unisce con il metodo scelto e Delete branch', async () => {
    detail({ role: 'write' });
    vi.mocked(api.mergePull).mockResolvedValue(full({ state: 'merged' }));
    const user = userEvent.setup();
    await user.click(await screen.findByLabelText('Delete branch after merge'));
    await user.click(screen.getByRole('button', { name: 'Squash and merge' }));
    await waitFor(() => expect(api.mergePull).toHaveBeenCalledWith({ owner: 'acme', repo: 'api', number: 58 }, 'squash', true, 'abc1234'));
    expect(screen.getByText('Looks good')).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: '#57' }).some((a) => a.getAttribute('href') === '/acme/api/issues/57')).toBe(true);
  }, 15000);

  it('con solo read il riquadro non ha pulsanti di merge ne Close, ma si puo commentare', async () => {
    detail({ role: 'read' });
    await screen.findByText('Looks good');
    await waitFor(() => expect(screen.getByText(/needs Write access/)).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: /merge/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Close pull request' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Comment' })).toBeInTheDocument();
  }, 15000);

  it('l autore senza write puo chiudere la propria PR', async () => {
    detail({ role: 'read', me: 'build-agent' });
    expect(await screen.findByRole('button', { name: 'Close pull request' })).toBeInTheDocument();
  }, 15000);

  it('unita: stato Merged e Delete branch', async () => {
    detail({ role: 'write', pr: full({ state: 'merged', mergedAt: '2026-10-18T12:00:00Z', mergedBy: person, mergeMethod: 'squash', mergeCommitSha: '9a1c3e7aaaa' }) });
    expect(await screen.findByText('Pull request successfully merged and closed')).toBeInTheDocument();
    expect(screen.getByText(/squashed and merged/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete branch' })).toBeInTheDocument();
    expect(api.fetchMergeability).not.toHaveBeenCalled();
  }, 15000);
});
