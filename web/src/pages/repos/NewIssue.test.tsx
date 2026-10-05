import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Issue, IssueTemplate, Role } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { NewIssuePage } from './NewIssuePage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchTeams: vi.fn(), fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/issuesApi', async (orig) => {
  const real = await orig<typeof import('../../lib/issuesApi')>();
  return {
    MAX_ATTACHMENT_BYTES: real.MAX_ATTACHMENT_BYTES,
    fetchTemplates: vi.fn(),
    createNewIssue: vi.fn(),
    searchUsers: vi.fn(),
    fetchMyRole: vi.fn(),
    fetchLabels: vi.fn(),
    fetchMilestones: vi.fn(),
    uploadAttachment: vi.fn(),
  };
});

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/issuesApi';
import { fetchTeams } from '../../lib/orgsApi';

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

const templates: IssueTemplate[] = [
  { name: 'Bug report', title: 'Bug: ', about: 'Something does not work.', labels: ['bug'], body: '## What happened\n' },
  { name: 'Agent task', about: 'Precise goal.', labels: ['agent-ready'], body: '## Goal\n' },
];

interface Setup {
  role?: Role;
  archived?: boolean;
  templates?: IssueTemplate[];
  owner?: Repository['owner'];
}

function setup(o: Setup = {}) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'lbianchi', isAdmin: false } } as never);
  vi.mocked(api.fetchMyRole).mockResolvedValue(o.role === undefined ? 'write' : o.role);
  vi.mocked(api.fetchTemplates).mockResolvedValue(o.templates ?? templates);
  render(
    <MemoryRouter initialEntries={['/acme/api/issues/new']}>
      <Routes>
        <Route path="/acme/api/issues/new" element={<NewIssuePage repo={{ ...repo, archived: o.archived ?? false, owner: o.owner ?? repo.owner }} />} />
        <Route path="/acme/api/issues/:number" element={<div>detail page</div>} />
        <Route path="/acme/api/issues" element={<div>list page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

const created = { number: 77 } as Issue;

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchLabels).mockResolvedValue({
    items: [
      { id: 'l1', name: 'bug', color: 'd73a4a', description: '', openIssues: 1 },
      { id: 'l2', name: 'agent-ready', color: 'ff8a3d', description: '', openIssues: 0 },
      { id: 'l3', name: 'question', color: '0075ca', description: '', openIssues: 0 },
    ],
    page: 1,
    perPage: 100,
    total: 3,
  });
  vi.mocked(api.fetchMilestones).mockResolvedValue({
    items: [{ number: 1, title: 'v0.5', description: '', state: 'open', openIssues: 5, closedIssues: 9, createdAt: '', updatedAt: '' }],
    page: 1,
    perPage: 30,
    total: 1,
  });
  vi.mocked(api.createNewIssue).mockResolvedValue(created);
  vi.mocked(api.searchUsers).mockResolvedValue([]);
  vi.mocked(fetchTeams).mockResolvedValue([]);
});

describe('NewIssuePage', () => {
  it('con un modello scelto precompila titolo e testo e applica le sue etichette', async () => {
    const user = userEvent.setup();
    setup();
    const group = await screen.findByRole('radiogroup', { name: 'Issue template' });
    expect(within(group).getAllByRole('radio')).toHaveLength(3);
    expect(screen.getByLabelText('Title')).toHaveValue('Bug: ');
    expect(screen.getByLabelText('Description')).toHaveValue('## What happened\n');
    expect(within(screen.getByRole('group', { name: 'Labels of this issue' })).getByLabelText('bug')).toBeChecked();

    await user.click(within(group).getByRole('radio', { name: /Agent task/ }));
    expect(screen.getByLabelText('Title')).toHaveValue('');
    expect(screen.getByLabelText('Description')).toHaveValue('## Goal\n');
    const labels = screen.getByRole('group', { name: 'Labels of this issue' });
    expect(within(labels).getByLabelText('bug')).not.toBeChecked();
    expect(within(labels).getByLabelText('agent-ready')).toBeChecked();

    await user.type(screen.getByLabelText('Title'), 'Do it');
    await user.click(screen.getByRole('button', { name: 'Submit new issue' }));
    await waitFor(() => expect(api.createNewIssue).toHaveBeenCalled());
    expect(api.createNewIssue).toHaveBeenCalledWith('acme', 'api', { title: 'Do it', body: '## Goal\n', labels: ['agent-ready'] });
    expect(await screen.findByText('detail page')).toBeInTheDocument();
  });

  it('Blank issue parte da titolo e testo vuoti, senza etichette', async () => {
    const user = userEvent.setup();
    setup();
    const group = await screen.findByRole('radiogroup', { name: 'Issue template' });
    await user.click(within(group).getByRole('radio', { name: /Blank issue/ }));
    expect(screen.getByLabelText('Title')).toHaveValue('');
    expect(screen.getByLabelText('Description')).toHaveValue('');
    expect(screen.getByRole('button', { name: 'Submit new issue' })).toBeDisabled();
    await user.type(screen.getByLabelText('Title'), 'Plain');
    await user.click(screen.getByRole('button', { name: 'Submit new issue' }));
    await waitFor(() => expect(api.createNewIssue).toHaveBeenCalledWith('acme', 'api', { title: 'Plain' }));
  });

  it('con la lista dei modelli vuota va dritto a Blank, senza la scelta', async () => {
    setup({ templates: [] });
    await screen.findByLabelText('Title');
    expect(screen.queryByRole('radiogroup', { name: 'Issue template' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Description')).toHaveValue('');
  });

  it('rifiuta un allegato oltre 10 MB senza caricarlo', async () => {
    const user = userEvent.setup();
    setup();
    await screen.findByLabelText('Title');
    const big = new File(['x'], 'huge.zip', { type: 'application/zip' });
    Object.defineProperty(big, 'size', { value: 10 * 1024 * 1024 + 1 });
    await user.upload(screen.getByLabelText('Attach files'), big);
    expect(await screen.findByText(/huge\.zip.*limited to 10 MB/)).toBeInTheDocument();
    expect(api.uploadAttachment).not.toHaveBeenCalled();
  });

  it('un allegato valido viene collegato alla issue con attachmentIds', async () => {
    const user = userEvent.setup();
    setup({ templates: [] });
    vi.mocked(api.uploadAttachment).mockResolvedValue({ id: 'a1', filename: 'trace.log', size: 2048 } as never);
    await screen.findByLabelText('Title');
    await user.upload(screen.getByLabelText('Attach files'), new File(['log'], 'trace.log', { type: 'text/plain' }));
    expect(await screen.findByText('trace.log')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Title'), 'With log');
    await user.click(screen.getByRole('button', { name: 'Submit new issue' }));
    await waitFor(() => expect(api.createNewIssue).toHaveBeenCalledWith('acme', 'api', { title: 'With log', attachmentIds: ['a1'] }));
  });

  it('con read niente assegnatari, etichette e milestone, ma la issue si apre', async () => {
    const user = userEvent.setup();
    setup({ role: 'read' });
    await screen.findByLabelText('Title');
    expect(screen.queryByText('Assignees', { selector: 'h4' })).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Labels of this issue' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Milestone')).not.toBeInTheDocument();
    expect(api.fetchLabels).not.toHaveBeenCalled();
    // il modello precompila ma le etichette non partono (servirebbe write)
    await user.type(screen.getByLabelText('Title'), 'x');
    await user.click(screen.getByRole('button', { name: 'Submit new issue' }));
    await waitFor(() => expect(api.createNewIssue).toHaveBeenCalledWith('acme', 'api', { title: 'Bug: x', body: '## What happened\n' }));
    expect(await screen.findByText('detail page')).toBeInTheDocument();
  });

  it('con write imposta assegnatari e milestone nella creazione', async () => {
    const user = userEvent.setup();
    setup({ templates: [] });
    await screen.findByLabelText('Title');
    await user.click(screen.getByRole('button', { name: 'Assign yourself' }));
    await user.selectOptions(screen.getByLabelText('Milestone'), 'v0.5');
    await user.click(within(screen.getByRole('group', { name: 'Labels of this issue' })).getByLabelText('question'));
    await user.type(screen.getByLabelText('Title'), 'Full');
    await user.click(screen.getByRole('button', { name: 'Submit new issue' }));
    await waitFor(() =>
      expect(api.createNewIssue).toHaveBeenCalledWith('acme', 'api', { title: 'Full', labels: ['question'], assignees: ['lbianchi'], milestone: 1 }),
    );
  });

  it('repo archiviato: niente form, solo l\'avviso', async () => {
    setup({ archived: true });
    expect(await screen.findByText(/archived: new issues cannot be opened/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Title')).not.toBeInTheDocument();
    expect(api.createNewIssue).not.toHaveBeenCalled();
  });

  describe('suggerimenti di @menzione', () => {
    const type = async (text: string) => {
      const user = userEvent.setup();
      setup({ templates: [] });
      await screen.findByLabelText('Title');
      await user.type(screen.getByLabelText('Description'), text);
      return user;
    };

    it('senza prefisso dopo «@» non chiama nessuna API e non suggerisce', async () => {
      await type('ciao @');
      expect(api.searchUsers).not.toHaveBeenCalled();
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });

    it('filtra persone, agenti e team per prefisso e li inserisce', async () => {
      vi.mocked(api.searchUsers).mockResolvedValue([
        { id: '1', username: 'mrossi', kind: 'human', displayName: 'Marco Rossi' },
        { id: '2', username: 'mbot', kind: 'agent', displayName: 'M bot' },
        { id: '3', username: 'lbianchi', kind: 'human', displayName: 'L' }, // fuori prefisso: scartato
        { id: '4', username: 'mold', kind: 'human', displayName: 'Old', isActive: false }, // disattivo: scartato
      ] as never);
      vi.mocked(fetchTeams).mockResolvedValue([
        { id: 't1', orgId: 'o', name: 'mobile', createdAt: '' },
        { id: 't2', orgId: 'o', name: 'devs', createdAt: '' },
      ]);
      const user = await type('ciao @m');
      const list = await screen.findByRole('listbox', { name: 'Mention suggestions' });
      expect(api.searchUsers).toHaveBeenCalledWith('m', 8);
      const names = within(list).getAllByRole('option').map((o) => o.textContent);
      expect(names.join('|')).toContain('@mrossi');
      expect(names.join('|')).toContain('@mbot');
      expect(names.join('|')).toContain('@acme/mobile');
      expect(names.join('|')).not.toContain('lbianchi');
      expect(names.join('|')).not.toContain('mold');
      expect(names.join('|')).not.toContain('devs');

      await user.keyboard('{ArrowDown}');
      await user.keyboard('{Enter}');
      expect(screen.getByLabelText('Description')).toHaveValue('ciao @mbot ');
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      // la menzione non assegna niente
      expect(screen.getByText('No one')).toBeInTheDocument();
    });

    it('Esc chiude i suggerimenti senza inserire nulla', async () => {
      vi.mocked(api.searchUsers).mockResolvedValue([{ id: '1', username: 'mrossi', kind: 'human', displayName: 'Marco' }] as never);
      const user = await type('@mr');
      await screen.findByRole('listbox');
      await user.keyboard('{Escape}');
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(screen.getByLabelText('Description')).toHaveValue('@mr');
    });

    it('@org/prefisso cerca solo tra i team dell\'organizzazione', async () => {
      vi.mocked(fetchTeams).mockResolvedValue([
        { id: 't1', orgId: 'o', name: 'mobile', createdAt: '' },
        { id: 't2', orgId: 'o', name: 'devs', createdAt: '' },
      ]);
      await type('@acme/de');
      const list = await screen.findByRole('listbox');
      expect(within(list).getAllByRole('option')).toHaveLength(1);
      expect(list).toHaveTextContent('@acme/devs');
      expect(vi.mocked(api.searchUsers).mock.calls.some(([q]) => q.includes('/'))).toBe(false);
    });

    it('repo di una persona: niente team', async () => {
      const user = userEvent.setup();
      setup({ templates: [], owner: { type: 'user', name: 'acme' } });
      await screen.findByLabelText('Title');
      vi.mocked(api.searchUsers).mockResolvedValue([{ id: '1', username: 'mrossi', kind: 'human', displayName: 'Marco' }] as never);
      await user.type(screen.getByLabelText('Description'), '@m');
      await screen.findByRole('listbox');
      expect(fetchTeams).not.toHaveBeenCalled();
    });
  });
});
