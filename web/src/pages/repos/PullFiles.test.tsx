import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../lib/http';
import type { PullRequest, PullRequestFiles, PullRequestThread } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { PullFilesPage } from './PullFilesPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/issuesApi', () => ({ fetchMyRole: vi.fn() }));
vi.mock('../../lib/pullsApi', () => ({
  fetchPull: vi.fn(),
  fetchPullFiles: vi.fn(),
  fetchPullThreads: vi.fn(),
  postLineComment: vi.fn(),
  submitReview: vi.fn(),
  setThreadResolved: vi.fn(),
  setFileViewed: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchMyRole } from '../../lib/issuesApi';
import * as api from '../../lib/pullsApi';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', fullName: 'acme/api', defaultBranch: 'main', archived: false, empty: false } as Repository;
const agent = { id: 'b', username: 'build-agent', kind: 'agent' as const };

const PATCH = ['@@ -10,3 +10,5 @@', ' ctx a', '-old b', '+new b', '+new c', '+new d', ' ctx e', ''].join('\n');

const pr = (o: Partial<PullRequest> = {}): PullRequest =>
  ({
    id: 'p58',
    number: 58,
    title: 'fix(webhooks): stop retrying',
    body: '',
    state: 'open',
    draft: false,
    author: agent,
    sourceBranch: 'agent/57',
    targetBranch: 'main',
    headSha: 'abc1234',
    commentCount: 5,
    changedFiles: 2,
    ...o,
  }) as PullRequest;

const files = (o: Partial<PullRequestFiles> = {}): PullRequestFiles => ({
  files: [
    { path: 'a.go', status: 'modified', additions: 3, deletions: 1, binary: false, truncated: false, patch: PATCH },
    { path: 'b_test.go', status: 'modified', additions: 1, deletions: 0, binary: false, truncated: false, patch: PATCH },
  ],
  filesChanged: 2,
  additions: 4,
  deletions: 1,
  truncated: false,
  viewedPaths: [],
  headSha: 'abc1234',
  baseSha: 'base000',
  ...o,
});

const thread = (o: Partial<PullRequestThread> = {}): PullRequestThread => ({
  id: 't1',
  path: 'a.go',
  side: 'new',
  line: 12,
  resolved: false,
  outdated: false,
  comments: [{ id: 't1', author: agent, body: 'Question', outdated: false, edited: false, deleted: false, createdAt: '2026-10-18T10:00:00Z', updatedAt: '2026-10-18T10:00:00Z' }],
  ...o,
});

function setup(o: { me?: { username: string; kind: 'human' | 'agent' }; role?: 'read' | 'write'; pr?: PullRequest; files?: PullRequestFiles; threads?: PullRequestThread[] } = {}) {
  const me = o.me ?? { username: 'lbianchi', kind: 'human' };
  vi.mocked(fetchSession).mockResolvedValue({ user: { ...me, isAdmin: false } } as never);
  vi.mocked(fetchMyRole).mockResolvedValue(o.role ?? 'write');
  vi.mocked(api.fetchPull).mockResolvedValue(o.pr ?? pr());
  vi.mocked(api.fetchPullFiles).mockResolvedValue(o.files ?? files());
  vi.mocked(api.fetchPullThreads).mockResolvedValue(o.threads ?? []);
  vi.mocked(api.submitReview).mockResolvedValue({} as never);
  vi.mocked(api.postLineComment).mockResolvedValue({} as never);
  vi.mocked(api.setFileViewed).mockResolvedValue(undefined);
  vi.mocked(api.setThreadResolved).mockResolvedValue(undefined);
  render(
    <MemoryRouter initialEntries={['/acme/api/pulls/58/files']}>
      <Routes>
        <Route path="/:owner/:repo/pulls/:number/files" element={<PullFilesPage repo={repo} />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ref = { owner: 'acme', repo: 'api', number: 58 };

beforeEach(() => {
  vi.clearAllMocks();
});

describe('Files changed', () => {
  it('mostra riepilogo, schede e conversazioni risolte', async () => {
    setup({ threads: [thread(), thread({ id: 't2', resolved: true })] });
    expect(await screen.findByText('2 files')).toBeInTheDocument();
    expect(screen.getByText(/1 of 2 conversations resolved/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Conversation/ })).toHaveAttribute('href', '/acme/api/pulls/58');
    expect(screen.getByRole('region', { name: 'Diff of a.go' })).toBeInTheDocument();
    expect(api.fetchPullFiles).toHaveBeenCalledWith(ref, false);
  }, 15000);

  it('Ignore whitespace e Split rileggono o ridisegnano il diff', async () => {
    setup();
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('checkbox', { name: 'Ignore whitespace' }));
    await waitFor(() => expect(api.fetchPullFiles).toHaveBeenLastCalledWith(ref, true));
    await user.click(screen.getByRole('button', { name: 'Split' }));
    expect(await screen.findAllByRole('table', { name: /Split diff of/ })).toHaveLength(2);
  }, 15000);

  it('commento su un gruppo di righe: invia path, lato, righe e punta della PR', async () => {
    setup();
    const user = userEvent.setup();
    const sec = await screen.findByRole('region', { name: 'Diff of a.go' });
    await user.click(within(sec).getByRole('button', { name: 'Comment on line 11' }));
    await user.keyboard('{Shift>}');
    await user.click(within(sec).getByRole('button', { name: 'Comment on line 13' }));
    await user.keyboard('{/Shift}');
    await user.type(within(sec).getByRole('textbox', { name: 'Comment on lines +11 to +13' }), 'Rework');
    await user.click(within(sec).getByRole('button', { name: 'Add comment' }));
    await waitFor(() =>
      expect(api.postLineComment).toHaveBeenCalledWith(ref, { path: 'a.go', side: 'new', line: 13, startLine: 11, body: 'Rework', commitSha: 'abc1234' }),
    );
    await waitFor(() => expect(api.fetchPullThreads).toHaveBeenCalledTimes(2));
  }, 15000);

  it('risposta e risoluzione di una conversazione', async () => {
    setup({ threads: [thread()] });
    const user = userEvent.setup();
    const t = await screen.findByRole('group', { name: 'Conversation on a.go line +12' });
    await user.type(within(t).getByRole('textbox', { name: 'Reply to build-agent' }), 'ok');
    await user.click(within(t).getByRole('button', { name: 'Reply' }));
    await waitFor(() => expect(api.postLineComment).toHaveBeenCalledWith(ref, { body: 'ok', inReplyTo: 't1' }));
    await user.click(within(t).getByRole('button', { name: 'Resolve conversation' }));
    await waitFor(() => expect(api.setThreadResolved).toHaveBeenCalledWith(ref, 't1', true));
  }, 15000);

  it('Viewed salva lo stato e un errore lo annulla', async () => {
    setup({ files: files({ viewedPaths: ['b_test.go'] }) });
    const user = userEvent.setup();
    const a = await screen.findByRole('region', { name: 'Diff of a.go' });
    expect(within(screen.getByRole('region', { name: 'Diff of b_test.go' })).getByText(/File marked as viewed/)).toBeInTheDocument();
    await user.click(within(a).getByRole('checkbox', { name: 'Viewed' }));
    await waitFor(() => expect(api.setFileViewed).toHaveBeenCalledWith(ref, 'a.go', true));
    expect(await within(a).findByText(/File marked as viewed/)).toBeInTheDocument();
  }, 15000);

  it('un errore su Viewed annulla la casella', async () => {
    setup();
    vi.mocked(api.setFileViewed).mockRejectedValueOnce(new ApiError({ error: { code: 'not_implemented', message: 'nope' } }, 501));
    const a = await screen.findByRole('region', { name: 'Diff of a.go' });
    await userEvent.setup().click(within(a).getByRole('checkbox', { name: 'Viewed' }));
    expect(await screen.findByText(/Reviews are not available on this server yet/)).toBeInTheDocument();
    await waitFor(() => expect(within(a).queryByText(/File marked as viewed/)).toBeNull());
    expect(within(a).getByRole('checkbox', { name: 'Viewed' })).not.toBeChecked();
  }, 15000);

  it.each([
    ['Comment', 'comment', 'Looks fine'],
    ['Approve', 'approve', ''],
    ['Request changes', 'request_changes', 'Please fix'],
  ] as const)('invia la review %s', async (label, event, text) => {
    setup();
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('button', { name: /Review changes/ }));
    const panel = screen.getByRole('dialog', { name: 'Finish your review' });
    if (text) await user.type(within(panel).getByRole('textbox', { name: 'Review summary' }), text);
    await user.click(within(panel).getByRole('radio', { name: new RegExp(`^${label}`) }));
    await user.click(within(panel).getByRole('button', { name: 'Submit review' }));
    await waitFor(() => expect(api.submitReview).toHaveBeenCalledWith(ref, event, text, 'abc1234'));
    expect(await screen.findByText('Review submitted.')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Finish your review' })).toBeNull();
  }, 15000);

  it('una review Comment senza testo non si invia', async () => {
    setup();
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('button', { name: /Review changes/ }));
    const panel = screen.getByRole('dialog', { name: 'Finish your review' });
    await user.click(within(panel).getByRole('radio', { name: /^Comment/ }));
    expect(within(panel).getByRole('button', { name: 'Submit review' })).toBeDisabled();
  }, 15000);

  it('l approvazione di un agente si vede con la nota che non conta', async () => {
    setup({ me: { username: 'review-bot', kind: 'agent' } });
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('button', { name: /Review changes/ }));
    const panel = screen.getByRole('dialog', { name: 'Finish your review' });
    expect(within(panel).getByRole('radio', { name: /^Approve/ })).toBeChecked();
    expect(within(panel).getByRole('note')).toHaveTextContent(/never counts/);
    await user.click(within(panel).getByRole('button', { name: 'Submit review' }));
    await waitFor(() => expect(api.submitReview).toHaveBeenCalledWith(ref, 'approve', '', 'abc1234'));
  }, 15000);

  it('l autore non puo approvare la propria PR', async () => {
    setup({ me: { username: 'build-agent', kind: 'agent' } });
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('button', { name: /Review changes/ }));
    const panel = screen.getByRole('dialog', { name: 'Finish your review' });
    expect(within(panel).getByRole('radio', { name: /^Approve/ })).toBeDisabled();
  }, 15000);

  it('con il backend ancora a 501 l errore e leggibile', async () => {
    setup();
    vi.mocked(api.submitReview).mockRejectedValue(new ApiError({ error: { code: 'not_implemented', message: 'x' } }, 501));
    const user = userEvent.setup();
    await screen.findByText('2 files');
    await user.click(screen.getByRole('button', { name: /Review changes/ }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Submit review' }));
    expect(await screen.findByText(/Reviews are not available on this server yet/)).toBeInTheDocument();
  }, 15000);

  it('oltre i limiti B6 mostra solo l elenco dei file', async () => {
    setup({ files: files({ listOnly: true, files: [{ path: 'a.go', status: 'modified', additions: 3, deletions: 1, binary: false, truncated: false }] }) });
    expect(await screen.findByText(/only the list of files is shown/)).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
  }, 15000);

  it('su una PR chiusa non ci sono commenti sulle righe', async () => {
    setup({ pr: pr({ state: 'closed' }) });
    await screen.findByText('2 files');
    expect(screen.queryByRole('button', { name: /Comment on/ })).toBeNull();
  }, 15000);
});
