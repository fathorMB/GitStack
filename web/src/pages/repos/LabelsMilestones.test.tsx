import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Label, Milestone } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/issuesApi', () => ({
  countIssues: vi.fn(),
  fetchMyRole: vi.fn(),
  fetchLabels: vi.fn(),
  addLabel: vi.fn(),
  editLabel: vi.fn(),
  removeLabel: vi.fn(),
  fetchMilestones: vi.fn(),
  addMilestone: vi.fn(),
  editMilestone: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { addLabel, addMilestone, countIssues, editLabel, editMilestone, fetchLabels, fetchMilestones, fetchMyRole, removeLabel } from '../../lib/issuesApi';
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

const bug: Label = { id: 'l1', name: 'bug', color: 'd73a4a', description: "Something isn't working", openIssues: 3 };
const ms = (o: Partial<Milestone>): Milestone => ({
  number: 1,
  title: 'v1.0',
  description: '',
  dueOn: '2026-12-01',
  state: 'open',
  openIssues: 1,
  closedIssues: 3,
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  ...o,
});

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/labels" element={<RepoPage mode="labels" />} />
        <Route path="/:owner/:repo/milestones" element={<RepoPage mode="milestones" />} />
      </Routes>
    </MemoryRouter>,
  );
}

function asRole(role: 'read' | 'write') {
  vi.mocked(fetchMyRole).mockResolvedValue(role);
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchRepo).mockResolvedValue(repo);
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'mrossi', isAdmin: false } } as never);
  vi.mocked(countIssues).mockResolvedValue(4);
  vi.mocked(fetchLabels).mockResolvedValue({ items: [bug], page: 1, perPage: 100, total: 1 });
  vi.mocked(fetchMilestones).mockResolvedValue({ items: [ms({}), ms({ number: 2, title: 'v0.9', state: 'closed', openIssues: 0, closedIssues: 2 })], page: 1, perPage: 30, total: 2 });
});

describe('Labels', () => {
  it('con write: crea, modifica ed elimina con conferma', async () => {
    asRole('write');
    vi.mocked(addLabel).mockResolvedValue(bug);
    vi.mocked(editLabel).mockResolvedValue(bug);
    vi.mocked(removeLabel).mockResolvedValue(undefined);
    const u = userEvent.setup();
    renderAt('/acme/api/labels');

    await u.click(await screen.findByRole('button', { name: 'New label' }));
    let form = screen.getByRole('form', { name: 'New label' });
    await u.type(within(form).getByLabelText('Label name'), 'p1');
    await u.clear(within(form).getByLabelText('Color'));
    await u.type(within(form).getByLabelText('Color'), 'FF0000');
    await u.type(within(form).getByLabelText('Description (optional)'), 'urgent');
    await u.click(within(form).getByRole('button', { name: 'Create label' }));
    await waitFor(() => expect(addLabel).toHaveBeenCalledWith('acme', 'api', { name: 'p1', color: 'ff0000', description: 'urgent' }));

    await u.click(await screen.findByRole('button', { name: 'Edit label bug' }));
    form = screen.getByRole('form', { name: 'Edit label' });
    await u.clear(within(form).getByLabelText('Label name'));
    await u.type(within(form).getByLabelText('Label name'), 'defect');
    await u.click(within(form).getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(editLabel).toHaveBeenCalledWith('acme', 'api', 'bug', { name: 'defect', color: 'd73a4a', description: "Something isn't working" }));

    await u.click(await screen.findByRole('button', { name: 'Delete label bug' }));
    expect(removeLabel).not.toHaveBeenCalled();
    await u.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete label' }));
    await waitFor(() => expect(removeLabel).toHaveBeenCalledWith('acme', 'api', 'bug'));
  });

  it('rifiuta un colore non valido senza chiamare l API', async () => {
    asRole('write');
    const u = userEvent.setup();
    renderAt('/acme/api/labels');
    await u.click(await screen.findByRole('button', { name: 'New label' }));
    const form = screen.getByRole('form', { name: 'New label' });
    await u.type(within(form).getByLabelText('Label name'), 'x');
    await u.clear(within(form).getByLabelText('Color'));
    await u.type(within(form).getByLabelText('Color'), 'zz');
    await u.click(within(form).getByRole('button', { name: 'Create label' }));
    expect(await within(form).findByText(/six hex digits, without/)).toBeInTheDocument();
    expect(addLabel).not.toHaveBeenCalled();
  });

  it('sola lettura senza write: elenco senza azioni', async () => {
    asRole('read');
    renderAt('/acme/api/labels');
    expect(await screen.findByText('bug')).toBeInTheDocument();
    await waitFor(() => expect(fetchMyRole).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: 'New label' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Edit label/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Delete label/ })).not.toBeInTheDocument();
  });
});

describe('Milestones', () => {
  it('con write: aperte e chiuse, avanzamento, crea, modifica e chiude', async () => {
    asRole('write');
    vi.mocked(addMilestone).mockResolvedValue(ms({}));
    vi.mocked(editMilestone).mockResolvedValue(ms({}));
    const u = userEvent.setup();
    renderAt('/acme/api/milestones');

    expect(await screen.findByText('v1.0')).toBeInTheDocument();
    expect(screen.getByText(/75% complete/)).toBeInTheDocument();
    expect(screen.getByText('Due 2026-12-01')).toBeInTheDocument();
    expect(screen.queryByText('v0.9')).not.toBeInTheDocument();

    await u.click(await screen.findByRole('button', { name: 'New milestone' }));
    let form = screen.getByRole('form', { name: 'New milestone' });
    await u.type(within(form).getByLabelText('Milestone title'), 'v2');
    await u.click(within(form).getByRole('button', { name: 'Create milestone' }));
    await waitFor(() => expect(addMilestone).toHaveBeenCalledWith('acme', 'api', { title: 'v2', description: '', dueOn: null }));

    await u.click(await screen.findByRole('button', { name: 'Edit milestone v1.0' }));
    form = screen.getByRole('form', { name: 'Edit milestone' });
    await u.clear(within(form).getByLabelText('Milestone title'));
    await u.type(within(form).getByLabelText('Milestone title'), 'v1.1');
    await u.click(within(form).getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(editMilestone).toHaveBeenCalledWith('acme', 'api', 1, { title: 'v1.1', description: '', dueOn: '2026-12-01' }));

    await u.click(await screen.findByRole('button', { name: 'Close milestone v1.0' }));
    await waitFor(() => expect(editMilestone).toHaveBeenCalledWith('acme', 'api', 1, { state: 'closed' }));

    await u.click(screen.getByRole('button', { name: /Closed \(1\)/ }));
    expect(screen.getByText('v0.9')).toBeInTheDocument();
  });

  it('sola lettura senza write', async () => {
    asRole('read');
    renderAt('/acme/api/milestones');
    expect(await screen.findByText('v1.0')).toBeInTheDocument();
    await waitFor(() => expect(fetchMyRole).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: 'New milestone' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Edit milestone/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Close milestone/ })).not.toBeInTheDocument();
  });
});
