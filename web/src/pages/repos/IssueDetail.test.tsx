import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../lib/http';
import type { Issue, IssueComment, IssueEvent, IssueUser, Role } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { IssueDetailPage } from './IssueDetailPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/issuesApi', async (orig) => {
  const real = await orig<typeof import('../../lib/issuesApi')>();
  return {
    MAX_ATTACHMENT_BYTES: real.MAX_ATTACHMENT_BYTES,
    fetchIssue: vi.fn(),
    fetchIssueEvents: vi.fn(),
    fetchIssueComments: vi.fn(),
    fetchMyRole: vi.fn(),
    fetchLabels: vi.fn(),
    fetchMilestones: vi.fn(),
    fetchIssueVersions: vi.fn(),
    fetchCommentVersions: vi.fn(),
    editIssue: vi.fn(),
    closeIssueWith: vi.fn(),
    reopen: vi.fn(),
    setHidden: vi.fn(),
    setLocked: vi.fn(),
    putAssignees: vi.fn(),
    putLabels: vi.fn(),
    putMilestone: vi.fn(),
    postComment: vi.fn(),
    editComment: vi.fn(),
    removeComment: vi.fn(),
    uploadAttachment: vi.fn(),
    downloadAttachment: vi.fn(),
  };
});

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/issuesApi';

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

const user = (username: string, kind: 'human' | 'agent' = 'human'): IssueUser => ({ id: username, username, kind });
const baseIssue = (o: Partial<Issue> = {}): Issue => ({
  id: 'i41',
  number: 41,
  title: 'Push over SSH fails',
  body: 'Pushing fails for ed25519 keys',
  state: 'open',
  author: user('gverdi'),
  labels: [{ id: 'l1', name: 'bug', color: 'd73a4a' }],
  assignees: [user('build-agent', 'agent')],
  milestone: { number: 1, title: 'v0.5', state: 'open' },
  locked: false,
  hidden: false,
  edited: false,
  commentCount: 2,
  createdAt: '2026-10-04T10:00:00Z',
  updatedAt: '2026-10-04T10:00:00Z',
  ...o,
});
const comment = (id: string, by: IssueUser, o: Partial<IssueComment> = {}): IssueComment => ({
  id,
  issueNumber: 41,
  body: `Text of ${id}`,
  author: by,
  edited: false,
  deleted: false,
  createdAt: '2026-10-04T11:00:00Z',
  updatedAt: '2026-10-04T11:00:00Z',
  ...o,
});
const event = (id: string, type: IssueEvent['type'], data: Record<string, unknown> = {}, actor = user('mrossi'), at = '2026-10-04T12:00:00Z'): IssueEvent => ({ id, type, actor, data, createdAt: at });

interface Setup {
  role?: Role;
  me?: string;
  isAdmin?: boolean;
  issue?: Issue;
  comments?: IssueComment[];
  events?: IssueEvent[];
  archived?: boolean;
}

function setup(o: Setup = {}) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: o.me ?? 'lbianchi', isAdmin: o.isAdmin ?? false } } as never);
  vi.mocked(api.fetchMyRole).mockResolvedValue(o.role === undefined ? 'read' : o.role);
  vi.mocked(api.fetchIssue).mockResolvedValue(o.issue ?? baseIssue());
  vi.mocked(api.fetchIssueEvents).mockResolvedValue({ items: o.events ?? [], page: 1, perPage: 100, total: 0 });
  vi.mocked(api.fetchIssueComments).mockResolvedValue({ items: o.comments ?? [], page: 1, perPage: 100, total: 0 });
  render(
    <MemoryRouter initialEntries={['/acme/api/issues/41']}>
      <Routes>
        <Route path="/:owner/:repo/issues/:number" element={<IssueDetailPage repo={{ ...repo, archived: o.archived ?? false }} />} />
      </Routes>
    </MemoryRouter>,
  );
}

const apiErr = (message: string) => new ApiError({ error: { code: 'x', message } }, 404);
const sidebar = () => screen.getByRole('complementary', { name: 'Issue sidebar' });
const ref = { owner: 'acme', repo: 'api', number: 41 };

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchLabels).mockResolvedValue({ items: [{ id: 'l1', name: 'bug', color: 'd73a4a', description: '', openIssues: 1 }, { id: 'l2', name: 'agent-ready', color: 'ff8a3d', description: '', openIssues: 0 }], page: 1, perPage: 100, total: 2 });
  vi.mocked(api.fetchMilestones).mockResolvedValue({
    items: [
      { number: 1, title: 'v0.5', description: '', state: 'open', openIssues: 5, closedIssues: 9, dueOn: '2026-10-15', createdAt: '', updatedAt: '' },
      { number: 2, title: 'v0.6', description: '', state: 'open', openIssues: 0, closedIssues: 0, createdAt: '', updatedAt: '' },
    ],
    page: 1,
    perPage: 30,
    total: 2,
  });
  vi.mocked(api.editIssue).mockResolvedValue(baseIssue());
  vi.mocked(api.postComment).mockResolvedValue(comment('new', user('lbianchi')));
  vi.mocked(api.closeIssueWith).mockResolvedValue(baseIssue());
  vi.mocked(api.reopen).mockResolvedValue(baseIssue());
  vi.mocked(api.putAssignees).mockResolvedValue(baseIssue());
  vi.mocked(api.putLabels).mockResolvedValue(baseIssue());
  vi.mocked(api.putMilestone).mockResolvedValue(baseIssue());
  vi.mocked(api.setHidden).mockResolvedValue(baseIssue());
  vi.mocked(api.setLocked).mockResolvedValue(baseIssue());
  vi.mocked(api.editComment).mockResolvedValue(comment('c1', user('lbianchi')));
  vi.mocked(api.removeComment).mockResolvedValue();
});

describe('via token', () => {
  it('mostra «via token <nome>» sul commento creato con un token e non su quello da sessione', async () => {
    setup({
      comments: [
        comment('c1', user('build-agent', 'agent'), { viaToken: { id: 't1', name: 'ci-runner' } }),
        comment('c2', user('mrossi')),
      ],
    });
    expect(await screen.findByText('Text of c1')).toBeInTheDocument();
    expect(within(screen.getByTestId('comment-c1')).getByTestId('via-token')).toHaveTextContent('via token ci-runner');
    expect(within(screen.getByTestId('comment-c2')).queryByTestId('via-token')).not.toBeInTheDocument();
  });

  it("mostra «via token <nome>» accanto all'autore della issue", async () => {
    setup({ issue: baseIssue({ author: user('build-agent', 'agent'), viaToken: { id: 't1', name: 'ci-runner' } }) });
    expect(await screen.findByText('Pushing fails for ed25519 keys')).toBeInTheDocument();
    expect(screen.getAllByTestId('via-token')[0]).toHaveTextContent('via token ci-runner');
  });

  it('niente «via token» per una issue aperta da sessione', async () => {
    setup({});
    expect(await screen.findByText('Pushing fails for ed25519 keys')).toBeInTheDocument();
    expect(screen.queryByTestId('via-token')).not.toBeInTheDocument();
  });
});

describe('cronologia', () => {
  it('mostra testo, commenti, eventi, badge agent e comment deleted; niente For agents', async () => {
    setup({
      comments: [comment('c1', user('build-agent', 'agent')), comment('c2', user('mrossi'), { deleted: true, body: '' })],
      events: [
        event('e1', 'labeled', { label: { name: 'agent-ready', color: 'ff8a3d' } }),
        event('e2', 'assigned', { assignee: { username: 'build-agent' } }),
        event('e3', 'closed', { reason: 'duplicate', duplicateOf: 12 }),
        event('e4', 'referenced', { commit: 'abc' }),
      ],
      issue: baseIssue({ state: 'closed', closeReason: 'duplicate', duplicateOf: 12 }),
    });
    expect(await screen.findByText('Pushing fails for ed25519 keys')).toBeInTheDocument();
    expect(screen.getByText('Text of c1')).toBeInTheDocument();
    expect(within(screen.getByTestId('comment-c1')).getByText('agent')).toBeInTheDocument();
    expect(screen.getByText(/comment deleted/)).toBeInTheDocument();
    expect(screen.getByTestId('event-labeled')).toHaveTextContent('agent-ready');
    expect(screen.getByTestId('event-assigned')).toHaveTextContent('assigned build-agent');
    expect(screen.getByTestId('event-closed')).toHaveTextContent('closed this as duplicate of #12');
    expect(screen.getAllByText('Closed as duplicate of #12').length).toBeGreaterThan(0);
    expect(screen.queryByTestId('event-referenced')).not.toBeInTheDocument();
    expect(screen.queryByText(/Unsubscribe/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Linked commits/)).not.toBeInTheDocument();
    expect(screen.queryByText(/For agents/)).not.toBeInTheDocument();
    expect(screen.queryByText(/gs issue view/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /New issue/ })).toHaveAttribute('href', '/acme/api/issues/new');
  });

  it('la sidebar mostra l\'avanzamento della milestone', async () => {
    setup();
    await screen.findByText('Pushing fails for ed25519 keys');
    expect(within(sidebar()).getByText('64% complete · 9 of 14 closed')).toBeInTheDocument();
    expect(within(sidebar()).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '64');
    expect(within(sidebar()).getByText('bug')).toHaveStyle({ '--lc': '#d73a4a' });
  });

  it('un errore di caricamento (404) e\' mostrato', async () => {
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'x', isAdmin: false } } as never);
    vi.mocked(api.fetchIssue).mockRejectedValue(apiErr('Issue not found'));
    vi.mocked(api.fetchIssueEvents).mockResolvedValue({ items: [], page: 1, perPage: 1, total: 0 });
    vi.mocked(api.fetchIssueComments).mockResolvedValue({ items: [], page: 1, perPage: 1, total: 0 });
    vi.mocked(api.fetchMyRole).mockResolvedValue('read');
    render(
      <MemoryRouter initialEntries={['/acme/api/issues/41']}>
        <Routes>
          <Route path="/:owner/:repo/issues/:number" element={<IssueDetailPage repo={repo} />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(await screen.findByRole('alert')).toHaveTextContent('Issue not found');
  });
});

describe('permessi: read (non autore)', () => {
  it('commenta ma non modifica, non chiude, non tocca la sidebar e non vede Admin', async () => {
    const u = userEvent.setup();
    setup({ role: 'read', comments: [comment('c1', user('mrossi'), { edited: true })] });
    await screen.findByText('Pushing fails for ed25519 keys');
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Edit comment/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Delete comment/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Close as|Reopen issue/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Edit (assignees|labels|milestone)$/ })).not.toBeInTheDocument();
    expect(screen.queryByText('Lock conversation')).not.toBeInTheDocument();
    expect(screen.queryByText('Hide issue')).not.toBeInTheDocument();
    // «edited» e' solo testo: le versioni sono per admin
    expect(screen.queryByRole('button', { name: 'edited' })).not.toBeInTheDocument();
    await u.type(screen.getByLabelText('Leave a comment'), 'Me too');
    await u.click(screen.getByRole('button', { name: 'Comment' }));
    await waitFor(() => expect(api.postComment).toHaveBeenCalledWith(ref, 'Me too', []));
  });
});

describe('permessi: autore con solo read', () => {
  it('modifica il proprio testo e il proprio commento, chiude e riapre', async () => {
    const u = userEvent.setup();
    setup({ role: 'read', me: 'gverdi', comments: [comment('c1', user('gverdi')), comment('c2', user('mrossi'))] });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.click(screen.getByRole('button', { name: 'Edit' }));
    const title = screen.getByLabelText('Title');
    await u.clear(title);
    await u.type(title, 'New title');
    await u.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.editIssue).toHaveBeenCalledWith(ref, { title: 'New title', body: 'Pushing fails for ed25519 keys' }));

    expect(screen.queryByRole('button', { name: 'Edit comment by mrossi' })).not.toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Edit comment by gverdi' }));
    const box = screen.getByLabelText('Edit comment');
    await u.clear(box);
    await u.type(box, 'Fixed text');
    await u.click(screen.getByRole('button', { name: 'Update comment' }));
    await waitFor(() => expect(api.editComment).toHaveBeenCalledWith(ref, 'c1', 'Fixed text'));

    await u.click(screen.getByRole('button', { name: 'Close as completed' }));
    await waitFor(() => expect(api.closeIssueWith).toHaveBeenCalledWith(ref, 'completed', undefined));
    // niente sidebar modificabile e niente eliminazione del commento altrui
    expect(screen.queryByRole('button', { name: /^Edit labels$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete comment by mrossi' })).not.toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Delete comment by gverdi' }));
    await u.click(await screen.findByRole('button', { name: 'Delete comment' }));
    await waitFor(() => expect(api.removeComment).toHaveBeenCalledWith(ref, 'c1'));
  });

  it('riapre una issue chiusa con il motivo mostrato (not planned)', async () => {
    const u = userEvent.setup();
    setup({ role: 'read', me: 'gverdi', issue: baseIssue({ state: 'closed', closeReason: 'not_planned' }) });
    expect(await screen.findByText('Closed as not planned')).toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Reopen issue' }));
    await waitFor(() => expect(api.reopen).toHaveBeenCalledWith(ref));
  });
});

describe('permessi: write', () => {
  it('chiude con il motivo duplicate of #n', async () => {
    const u = userEvent.setup();
    setup({ role: 'write' });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.click(screen.getByRole('button', { name: 'Other close reasons' }));
    await u.click(screen.getByRole('menuitemradio', { name: /Close as duplicate of/ }));
    const close = screen.getByRole('button', { name: 'Close as duplicate of…' });
    expect(close).toBeDisabled();
    await u.type(screen.getByLabelText('Duplicate of issue number'), '41');
    expect(close).toBeDisabled();
    await u.clear(screen.getByLabelText('Duplicate of issue number'));
    await u.type(screen.getByLabelText('Duplicate of issue number'), '12');
    await u.click(screen.getByRole('button', { name: 'Close as duplicate of #12' }));
    await waitFor(() => expect(api.closeIssueWith).toHaveBeenCalledWith(ref, 'duplicate', 12));
  });

  it('chiude con not planned e un commento', async () => {
    const u = userEvent.setup();
    setup({ role: 'write' });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.click(screen.getByRole('button', { name: 'Other close reasons' }));
    await u.click(screen.getByRole('menuitemradio', { name: /Close as not planned/ }));
    await u.type(screen.getByLabelText('Leave a comment'), 'Out of scope');
    await u.click(screen.getByRole('button', { name: 'Close as not planned' }));
    await waitFor(() => expect(api.postComment).toHaveBeenCalledWith(ref, 'Out of scope', []));
    expect(api.closeIssueWith).toHaveBeenCalledWith(ref, 'not_planned', undefined);
  });

  it('modifica etichette, milestone e assegnatari; niente Admin', async () => {
    const u = userEvent.setup();
    setup({ role: 'write', me: 'mrossi' });
    await screen.findByText('Pushing fails for ed25519 keys');
    expect(screen.queryByText('Lock conversation')).not.toBeInTheDocument();

    await u.click(screen.getByRole('button', { name: 'Edit labels' }));
    await u.click(within(screen.getByRole('group', { name: 'Labels of this issue' })).getByLabelText('agent-ready'));
    await waitFor(() => expect(api.putLabels).toHaveBeenCalledWith(ref, ['bug', 'agent-ready']));

    await u.click(screen.getByRole('button', { name: 'Edit milestone' }));
    await u.selectOptions(screen.getByLabelText('Milestone'), 'v0.6');
    await waitFor(() => expect(api.putMilestone).toHaveBeenCalledWith(ref, 2));
    await u.selectOptions(screen.getByLabelText('Milestone'), '');
    await waitFor(() => expect(api.putMilestone).toHaveBeenLastCalledWith(ref, null));

    await u.click(screen.getByRole('button', { name: 'Edit assignees' }));
    await u.type(screen.getByLabelText('Assignee username'), 'lbianchi');
    await u.click(screen.getByRole('button', { name: 'Assign' }));
    await waitFor(() => expect(api.putAssignees).toHaveBeenCalledWith(ref, ['build-agent', 'lbianchi']));
    await u.click(screen.getByRole('button', { name: 'Assign yourself' }));
    await waitFor(() => expect(api.putAssignees).toHaveBeenLastCalledWith(ref, ['build-agent', 'mrossi']));
    await u.click(screen.getByRole('button', { name: 'Unassign build-agent' }));
    await waitFor(() => expect(api.putAssignees).toHaveBeenLastCalledWith(ref, []));
  });

  it('un errore dell\'API (422) resta visibile', async () => {
    const u = userEvent.setup();
    vi.mocked(api.putLabels).mockRejectedValue(apiErr('label does not exist'));
    setup({ role: 'write' });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.click(screen.getByRole('button', { name: 'Edit labels' }));
    await u.click(screen.getByLabelText('agent-ready'));
    expect(await screen.findByRole('alert')).toHaveTextContent('label does not exist');
  });
});

describe('permessi: admin', () => {
  it('blocca la discussione e nasconde la issue con conferma', async () => {
    const u = userEvent.setup();
    setup({ role: 'admin' });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.click(screen.getByRole('button', { name: /Lock conversation/ }));
    await waitFor(() => expect(api.setLocked).toHaveBeenCalledWith(ref, true));
    await u.click(screen.getByRole('button', { name: /Hide issue/ }));
    expect(api.setHidden).not.toHaveBeenCalled();
    await u.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Hide issue' }));
    await waitFor(() => expect(api.setHidden).toHaveBeenCalledWith(ref, true));
  });

  it('sblocca e mostra una issue nascosta', async () => {
    const u = userEvent.setup();
    setup({ role: 'admin', issue: baseIssue({ locked: true, hidden: true }) });
    expect(await screen.findByText(/This issue is hidden/)).toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: /Unlock conversation/ }));
    await waitFor(() => expect(api.setLocked).toHaveBeenCalledWith(ref, false));
    await u.click(within(sidebar()).getByRole('button', { name: /Unhide issue/ }));
    await waitFor(() => expect(api.setHidden).toHaveBeenCalledWith(ref, false));
  });

  it('vede le versioni precedenti del testo e dei commenti e elimina il commento altrui', async () => {
    const u = userEvent.setup();
    vi.mocked(api.fetchIssueVersions).mockResolvedValue([{ version: 1, title: 'Old title', body: 'Original text', editor: user('gverdi'), createdAt: '2026-10-04T10:30:00Z' }]);
    vi.mocked(api.fetchCommentVersions).mockResolvedValue([{ version: 1, body: 'Old comment', editor: user('mrossi'), createdAt: '2026-10-04T11:30:00Z' }]);
    setup({ role: 'admin', issue: baseIssue({ edited: true }), comments: [comment('c1', user('mrossi'), { edited: true })] });
    await screen.findByText('Pushing fails for ed25519 keys');
    const edited = screen.getAllByRole('button', { name: 'edited' });
    await u.click(edited[0]);
    expect(await screen.findByText('Original text')).toBeInTheDocument();
    expect(api.fetchIssueVersions).toHaveBeenCalledWith(ref);
    await u.click(edited[1]);
    expect(await screen.findByText('Old comment')).toBeInTheDocument();
    expect(api.fetchCommentVersions).toHaveBeenCalledWith(ref, 'c1');
    await u.click(screen.getByRole('button', { name: 'Delete comment by mrossi' }));
    await u.click(await screen.findByRole('button', { name: 'Delete comment' }));
    await waitFor(() => expect(api.removeComment).toHaveBeenCalledWith(ref, 'c1'));
  });

  it("l'amministratore dell'installazione ha i comandi admin anche senza grant", async () => {
    setup({ role: null, isAdmin: true });
    await screen.findByText('Pushing fails for ed25519 keys');
    expect(screen.getByRole('button', { name: /Lock conversation/ })).toBeInTheDocument();
  });
});

describe('issue bloccata', () => {
  it('chi ha solo read non commenta; chi ha write si', async () => {
    setup({ role: 'read', issue: baseIssue({ locked: true }) });
    expect(await screen.findByText(/This conversation has been locked/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Leave a comment')).not.toBeInTheDocument();
  });

  it("l'autore con read non puo' modificare il testo di una issue bloccata", async () => {
    setup({ role: 'read', me: 'gverdi', issue: baseIssue({ locked: true }), comments: [comment('c1', user('gverdi'))] });
    await screen.findByText(/This conversation has been locked/);
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit comment by gverdi' })).not.toBeInTheDocument();
  });

  it('write commenta comunque', async () => {
    setup({ role: 'write', issue: baseIssue({ locked: true }) });
    expect(await screen.findByLabelText('Leave a comment')).toBeInTheDocument();
    expect(screen.getByText('Locked')).toBeInTheDocument();
  });
});

describe('repo archiviato', () => {
  it('tutto in sola lettura, anche per admin', async () => {
    setup({ role: 'admin', me: 'gverdi', archived: true, comments: [comment('c1', user('gverdi'))] });
    expect(await screen.findByText(/This repository has been archived/)).toBeInTheDocument();
    expect(screen.getByText(/commenting, closing and reopening are disabled/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Leave a comment')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Edit comment|Delete comment/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Close as|Reopen issue/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Edit (assignees|labels|milestone)$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Lock conversation|Hide issue/ })).not.toBeInTheDocument();
  });
});

describe('issue nascosta', () => {
  it('per admin mostra il contenuto con l\'avviso', async () => {
    setup({ role: 'admin', issue: baseIssue({ hidden: true }) });
    expect(await screen.findByText('Pushing fails for ed25519 keys')).toBeInTheDocument();
    expect(screen.getByText(/Only repository admins can see its content/)).toBeInTheDocument();
  });

  it('per chi non e\' admin l\'API risponde 404 e la pagina non mostra nulla', async () => {
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'x', isAdmin: false } } as never);
    vi.mocked(api.fetchIssue).mockRejectedValue(apiErr('Not found'));
    vi.mocked(api.fetchIssueEvents).mockRejectedValue(apiErr('Not found'));
    vi.mocked(api.fetchIssueComments).mockRejectedValue(apiErr('Not found'));
    vi.mocked(api.fetchMyRole).mockResolvedValue('write');
    render(
      <MemoryRouter initialEntries={['/acme/api/issues/41']}>
        <Routes>
          <Route path="/:owner/:repo/issues/:number" element={<IssueDetailPage repo={repo} />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(await screen.findByRole('alert')).toHaveTextContent('Not found');
    expect(screen.queryByText('Pushing fails for ed25519 keys')).not.toBeInTheDocument();
  });
});

describe('editor e allegati', () => {
  it("Preview mostra il Markdown e l'allegato viene collegato al commento", async () => {
    const u = userEvent.setup();
    vi.mocked(api.uploadAttachment).mockResolvedValue({ id: 'a1', filename: 'trace.log', contentType: 'text/plain', size: 2048, createdAt: '' });
    setup({ role: 'write' });
    await screen.findByText('Pushing fails for ed25519 keys');
    await u.type(screen.getByLabelText('Leave a comment'), 'See **log**');
    await u.click(screen.getByRole('tab', { name: 'Preview' }));
    expect(await screen.findByText('log', { selector: 'strong' }, { timeout: 15000 })).toBeInTheDocument();
    await u.click(screen.getByRole('tab', { name: 'Write' }));
    await u.upload(screen.getByLabelText('Attach files'), new File(['x'], 'trace.log', { type: 'text/plain' }));
    expect(await screen.findByText('trace.log')).toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Comment' }));
    await waitFor(() => expect(api.postComment).toHaveBeenCalledWith(ref, 'See **log**', ['a1']));
  }, 30000);

  it('rifiuta un allegato oltre 10 MB con un messaggio', async () => {
    const u = userEvent.setup();
    setup({ role: 'write' });
    await screen.findByText('Pushing fails for ed25519 keys');
    const big = new File(['x'], 'big.zip', { type: 'application/zip' });
    Object.defineProperty(big, 'size', { value: 11 * 1024 * 1024 });
    await u.upload(screen.getByLabelText('Attach files'), big);
    expect(await screen.findByText(/limited to 10 MB/)).toBeInTheDocument();
    expect(api.uploadAttachment).not.toHaveBeenCalled();
  });
});

describe('varianti del mockup 13', () => {
  it('normal: owner, issue chiusa, comment deleted, riapertura e nessun For agents', async () => {
    setup({ role: 'admin', me: 'mrossi', issue: baseIssue({ state: 'closed', closeReason: 'completed' }), comments: [comment('c1', user('mrossi'), { deleted: true, body: '' })] });
    expect(await screen.findByText('Closed as completed')).toBeInTheDocument();
    expect(screen.getByText(/comment deleted/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reopen issue' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /New issue/ })).toBeInTheDocument();
    expect(screen.queryByText(/For agents/)).not.toBeInTheDocument();
    expect(screen.queryByText(/access: you can open issues/)).not.toBeInTheDocument();
  });

  it('close: pulsante diviso con le tre voci e le descrizioni', async () => {
    const u = userEvent.setup();
    setup({ role: 'write', me: 'mrossi' });
    await screen.findByText('Pushing fails for ed25519 keys');
    expect(screen.getByRole('button', { name: 'Close as completed' })).toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Other close reasons' }));
    const menu = screen.getByRole('menu', { name: 'Close reasons' });
    expect(within(menu).getByText('Done, fixed, shipped. Counts toward milestone progress.')).toBeInTheDocument();
    expect(within(menu).getByText("Won't fix, can't reproduce, stale. Not counted in milestone progress.")).toBeInTheDocument();
    expect(within(menu).getByText('Close as duplicate of…')).toBeInTheDocument();
    await u.click(within(menu).getByRole('menuitemradio', { name: /Close as not planned/ }));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Close as not planned' }));
    await waitFor(() => expect(api.closeIssueWith).toHaveBeenCalledWith(ref, 'not_planned', undefined));
  });

  it("close: lo stesso controllo c'e' anche senza editor (commento vietato, chiusura permessa)", async () => {
    const u = userEvent.setup();
    setup({ role: 'read', me: 'gverdi', issue: baseIssue({ locked: true }) });
    await screen.findByText(/This conversation has been locked/);
    expect(screen.queryByLabelText('Leave a comment')).not.toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'Other close reasons' }));
    await u.click(screen.getByRole('menuitemradio', { name: /Close as duplicate of/ }));
    await u.type(screen.getByLabelText('Duplicate of issue number'), '7');
    await u.click(screen.getByRole('button', { name: 'Close as duplicate of #7' }));
    await waitFor(() => expect(api.closeIssueWith).toHaveBeenCalledWith(ref, 'duplicate', 7));
  });

  it('locked: vista read con alert-info, nota Read access, niente ingranaggi ne Admin', async () => {
    setup({ role: 'read', issue: baseIssue({ locked: true }), events: [event('e1', 'locked')] });
    const b = await screen.findByText('This conversation has been locked.');
    expect(b.closest('.alert')).toHaveClass('alert-info');
    expect(screen.getByText(/Only people with Write access to this repository can comment/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Leave a comment')).not.toBeInTheDocument();
    expect(within(sidebar()).getByText(/access: you can open issues and comment/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Edit (assignees|labels|milestone)$/ })).not.toBeInTheDocument();
    expect(within(sidebar()).queryByText('Admin')).not.toBeInTheDocument();
    expect(screen.queryByText(/For agents/)).not.toBeInTheDocument();
  });

  it("hidden: vista admin con alert-warning, chi l'ha nascosta, anteprima e Unhide issue", async () => {
    const u = userEvent.setup();
    setup({ role: 'admin', issue: baseIssue({ hidden: true }), events: [event('e1', 'hidden', {}, user('mrossi'))] });
    const b = await screen.findByText('This issue is hidden.');
    const alert = b.closest('.alert') as HTMLElement;
    expect(alert).toHaveClass('alert-warning');
    expect(alert).toHaveTextContent(/Hidden by mrossi/);
    expect(alert).toHaveTextContent('What everyone else sees at this address:');
    expect(alert).toHaveTextContent('#41 · This issue has been hidden by a repository admin.');
    expect(alert).toHaveTextContent('the number #41 is kept and never reused');
    await u.click(within(alert).getByRole('button', { name: 'Unhide issue' }));
    await waitFor(() => expect(api.setHidden).toHaveBeenCalledWith(ref, false));
  });

  it("hidden: senza evento la frase non ha Hidden by; chi non e' admin non ha il pulsante", async () => {
    setup({ role: 'write', issue: baseIssue({ hidden: true }) });
    const b = await screen.findByText('This issue is hidden.');
    const alert = b.closest('.alert') as HTMLElement;
    expect(alert).not.toHaveTextContent(/Hidden by/);
    expect(within(alert).queryByRole('button', { name: 'Unhide issue' })).not.toBeInTheDocument();
  });

  it('archived: avvisi in testata e in fondo, nessun editor, chiusura, Edit ne New issue; nota Read access', async () => {
    setup({ role: 'read', me: 'gverdi', archived: true, issue: baseIssue({ state: 'closed', closeReason: 'completed' }) });
    const head = await screen.findByText('This repository has been archived.');
    expect(head.closest('.alert')).toHaveClass('alert-warning');
    expect(head.closest('.alert')).toHaveTextContent('An admin can unarchive it from Settings.');
    const foot = screen.getByText('The repository is archived: commenting, closing and reopening are disabled.');
    expect(foot.closest('.alert')).toHaveClass('alert-warning');
    expect(screen.queryByLabelText('Leave a comment')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Close as|Other close reasons|Reopen issue|^Edit$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /New issue/ })).not.toBeInTheDocument();
    expect(within(sidebar()).getByText(/access: you can open issues and comment/)).toBeInTheDocument();
  });
});
