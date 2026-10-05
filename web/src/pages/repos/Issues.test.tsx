import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { IssueSummary } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/issuesApi', () => ({ fetchIssues: vi.fn(), countIssues: vi.fn(), fetchLabels: vi.fn(), fetchMilestones: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import { countIssues, fetchIssues, fetchLabels, fetchMilestones } from '../../lib/issuesApi';
import { fetchRepo } from '../../lib/reposApi';

const repo = {
  id: 'r1',
  owner: { type: 'organization', name: 'acme' },
  name: 'api',
  fullName: 'acme/api',
  description: '',
  visibility: 'private',
  defaultBranch: 'main',
  archived: false,
  empty: false,
  cloneUrls: { https: 'h', ssh: 's' },
} as Repository;

const user = (username: string, kind: 'human' | 'agent' = 'human') => ({ id: username, username, kind });
const issue = (n: number, o: Partial<IssueSummary> = {}): IssueSummary => ({
  number: n,
  title: `Issue ${n}`,
  state: 'open',
  author: user('mrossi'),
  labels: [],
  assignees: [],
  commentCount: 0,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
  ...o,
});

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search}</div>;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/issues" element={<RepoPage mode="issues" />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

const page = (items: IssueSummary[]) => ({ items, page: 1, perPage: 30, total: items.length });

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchRepo).mockResolvedValue(repo);
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'mrossi', isAdmin: false } } as never);
  vi.mocked(fetchIssues).mockResolvedValue(page([]));
  vi.mocked(countIssues).mockImplementation(async (_o, _r, q) => (q.includes('is:closed') ? 48 : 12));
  vi.mocked(fetchLabels).mockResolvedValue({ items: [{ id: 'l1', name: 'good first issue', color: '1a8f4c', description: '', openIssues: 1 }], page: 1, perPage: 100, total: 9 });
  vi.mocked(fetchMilestones).mockResolvedValue({ items: [{ number: 1, title: 'v0.5' } as never], page: 1, perPage: 30, total: 2 });
});

describe('lista issues', () => {
  it('mostra la scheda con il conteggio e la lista aperta', async () => {
    vi.mocked(fetchIssues).mockResolvedValue(
      page([
        issue(57, { labels: [{ id: 'l', name: 'bug', color: 'd73a4a' }], milestone: { number: 1, title: 'v0.5', state: 'open' }, commentCount: 3, assignees: [user('build-agent', 'agent')] }),
      ]),
    );
    renderAt('/acme/api/issues');
    const row = await screen.findByTestId('issue-57');
    expect(within(row).getByText('bug')).toHaveStyle({ '--lc': '#d73a4a' });
    expect(within(row).getByText(/v0.5/)).toBeInTheDocument();
    expect(within(row).getByText('agent')).toBeInTheDocument();
    expect(within(row).getByText('3')).toBeInTheDocument();
    expect(within(row).getByRole('img', { name: 'Open' })).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: /Issues 12/ })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /Open 12/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Closed 48/ })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Labels/ })).toHaveAttribute('href', '/acme/api/labels');
    expect(screen.getByRole('link', { name: /Milestones/ })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /New issue/ })).toHaveAttribute('href', '/acme/api/issues/new');
    expect(vi.mocked(fetchIssues).mock.calls[0][2]).toBe('is:open');
  });

  it('usa icone diverse per completed e per not_planned/duplicate', async () => {
    vi.mocked(fetchIssues).mockResolvedValue(
      page([
        issue(1, { state: 'closed', closeReason: 'completed' }),
        issue(2, { state: 'closed', closeReason: 'not_planned' }),
        issue(3, { state: 'closed', closeReason: 'duplicate' }),
      ]),
    );
    renderAt('/acme/api/issues?q=is%3Aclosed');
    expect(await within(await screen.findByTestId('issue-1')).findByRole('img', { name: 'Closed as completed' })).toBeInTheDocument();
    expect(within(screen.getByTestId('issue-2')).getByRole('img', { name: 'Closed as not planned' })).toBeInTheDocument();
    expect(within(screen.getByTestId('issue-3')).getByRole('img', { name: 'Closed as duplicate' })).toBeInTheDocument();
    expect(vi.mocked(fetchIssues).mock.calls[0][2]).toBe('is:closed');
  });

  it('i filtri compongono la ricerca e l\'indirizzo', async () => {
    const u = userEvent.setup();
    renderAt('/acme/api/issues');
    await screen.findByRole('button', { name: /Open 12/ });
    await u.selectOptions(screen.getByLabelText('Label'), 'good first issue');
    await waitFor(() => expect(screen.getByLabelText('Search issues')).toHaveValue('is:open label:"good first issue" '));
    expect(decodeURIComponent((screen.getByTestId('where').textContent ?? '').replace(/[+]/g, ' '))).toContain('q=is:open label:"good first issue"');
    await u.selectOptions(screen.getByLabelText('Milestone'), 'v0.5');
    await u.selectOptions(screen.getByLabelText('Assignee'), '@agents');
    await u.selectOptions(screen.getByLabelText('Sort'), 'comments');
    await waitFor(() => expect(screen.getByLabelText('Search issues')).toHaveValue('is:open label:"good first issue" milestone:v0.5 assignee:@agents '));
    expect(screen.getByTestId('where').textContent).toContain('sort=comments');
    const last = vi.mocked(fetchIssues).mock.calls.at(-1)!;
    expect(last[2]).toBe('is:open label:"good first issue" milestone:v0.5 assignee:@agents');
    expect(last[3]).toBe('comments');
    await u.click(screen.getByRole('button', { name: /Closed/ }));
    await waitFor(() => expect(screen.getByLabelText('Search issues')).toHaveValue('label:"good first issue" milestone:v0.5 assignee:@agents is:closed '));
  });

  it("la ricerca dall'indirizzo riempie la barra e interroga l'API", async () => {
    renderAt('/acme/api/issues?q=' + encodeURIComponent('is:open label:bug crash') + '&sort=relevance');
    expect(await screen.findByLabelText('Search issues')).toHaveValue('is:open label:bug crash ');
    expect(fetchIssues).toHaveBeenCalledWith('acme', 'api', 'is:open label:bug crash', 'relevance');
    expect(screen.getByLabelText('Label')).toHaveValue('bug');
  });

  it("scrivere nella barra e premere Invio aggiorna l'indirizzo", async () => {
    const u = userEvent.setup();
    renderAt('/acme/api/issues');
    const bar = await screen.findByLabelText('Search issues');
    await screen.findByRole("button", { name: /Open 12/ });
    await u.clear(bar);
    expect(bar).toHaveValue("");
    await u.type(bar, 'is:closed reason:duplicate{Enter}');
    await waitFor(() => expect(decodeURIComponent((screen.getByTestId('where').textContent ?? '').replace(/[+]/g, ' '))).toContain('q=is:closed reason:duplicate'));
    expect(await screen.findByText('No issues match your search.')).toBeInTheDocument();
  });
});
