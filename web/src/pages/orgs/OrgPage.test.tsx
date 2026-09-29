import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { OrgPage } from './OrgPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({
  fetchOrganization: vi.fn(),
  fetchOrgMembers: vi.fn(),
  fetchTeams: vi.fn(),
  saveOrgMember: vi.fn(),
  deleteOrgMember: vi.fn(),
  createOrgTeam: vi.fn(),
  deleteOrgTeam: vi.fn(),
  fetchTeamMembers: vi.fn(),
  saveTeamMember: vi.fn(),
  deleteTeamMember: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/orgsApi';

const user = (username: string, extra = {}) => ({
  id: `u-${username}`,
  username,
  kind: 'human' as const,
  displayName: username.toUpperCase(),
  ...extra,
});
const orgMember = (username: string, role: 'owner' | 'member') => ({
  user: user(username),
  role,
  createdAt: '2026-01-01T00:00:00Z',
});
const teamMember = (username: string, role: 'maintainer' | 'member') => ({
  user: user(username),
  role,
  createdAt: '2026-01-01T00:00:00Z',
});

function setup(me: ReturnType<typeof user>) {
  vi.mocked(fetchSession).mockResolvedValue({ user: me } as never);
  vi.mocked(api.fetchOrganization).mockResolvedValue({
    id: 'o1',
    name: 'acme',
    displayName: 'Acme',
    createdAt: '2026-01-01T00:00:00Z',
  });
  vi.mocked(api.fetchOrgMembers).mockResolvedValue([orgMember('boss', 'owner'), orgMember('dev', 'member')]);
  vi.mocked(api.fetchTeams).mockResolvedValue([
    { id: 't1', orgId: 'o1', name: 'platform', createdAt: '2026-01-01T00:00:00Z' },
  ]);
  vi.mocked(api.fetchTeamMembers).mockResolvedValue([teamMember('dev', 'member')]);
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/orgs/acme']}>
        <Routes>
          <Route path="/orgs/:org" element={<OrgPage />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('OrgPage', () => {
  beforeEach(() => {
    Object.values(api).forEach((f) => (f as ReturnType<typeof vi.fn>).mockReset());
  });

  it('owner: vede le azioni di gestione', async () => {
    setup(user('boss'));
    renderPage();
    expect(await screen.findByRole('button', { name: /new team/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /delete team/i })).toBeInTheDocument();
    expect(await screen.findByRole('form', { name: 'Add team member' })).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('tab', { name: /members/i }));
    expect(screen.getByRole('form', { name: 'Add member' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Role of dev' })).toBeInTheDocument();
  });

  it('member: vede la pagina ma senza azioni', async () => {
    setup(user('dev'));
    renderPage();
    expect(await screen.findByText('platform', { selector: 'b' })).toBeInTheDocument();
    await screen.findByText('DEV');
    expect(screen.queryByRole('button', { name: /new team/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /delete team/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'Add team member' })).not.toBeInTheDocument();
    const u = userEvent.setup();
    await u.click(screen.getByRole('tab', { name: /members/i }));
    expect(screen.queryByRole('form', { name: 'Add member' })).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: /role of/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Remove boss' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Leave dev' })).toBeInTheDocument();
  });

  it('maintainer del team: gestisce i membri del team ma non crea team', async () => {
    setup(user('dev'));
    vi.mocked(api.fetchTeamMembers).mockResolvedValue([teamMember('dev', 'maintainer')]);
    renderPage();
    expect(await screen.findByRole('form', { name: 'Add team member' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /new team/i })).not.toBeInTheDocument();
  });

  it('admin di sistema vede tutto anche senza essere owner', async () => {
    setup(user('root', { isAdmin: true }));
    renderPage();
    expect(await screen.findByRole('button', { name: /new team/i })).toBeInTheDocument();
  });

  it("toglie l'ultimo owner: mostra l'errore 409 del backend", async () => {
    setup(user('boss'));
    vi.mocked(api.deleteOrgMember).mockRejectedValue(
      new ApiError({ error: { code: 'last_owner', message: 'An organization needs at least one owner.' } }, 409),
    );
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('tab', { name: /members/i }));
    await u.click(screen.getByRole('button', { name: 'Remove boss' }));
    await u.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Remove' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('An organization needs at least one owner.');
    expect(api.deleteOrgMember).toHaveBeenCalledWith('acme', 'boss');
  });

  it('cambio di ruolo rifiutato con 409 last_owner', async () => {
    setup(user('boss'));
    vi.mocked(api.saveOrgMember).mockRejectedValue(
      new ApiError({ error: { code: 'last_owner', message: 'Cannot demote the last owner.' } }, 409),
    );
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('tab', { name: /members/i }));
    await u.selectOptions(screen.getByRole('combobox', { name: 'Role of boss' }), 'member');
    expect(await screen.findByRole('alert')).toHaveTextContent('Cannot demote the last owner.');
  });

  it('un 403 del backend diventa un messaggio di permessi', async () => {
    setup(user('boss'));
    vi.mocked(api.saveOrgMember).mockRejectedValue(
      new ApiError({ error: { code: 'forbidden', message: 'Owners only.' } }, 403),
    );
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('tab', { name: /members/i }));
    await u.type(screen.getByLabelText('Username'), 'newbie');
    await u.click(within(screen.getByRole('form', { name: 'Add member' })).getByRole('button', { name: /add member/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent("You don't have permission to do this. Owners only.");
    expect(api.saveOrgMember).toHaveBeenCalledWith('acme', 'newbie', 'member');
  });

  it('crea un team con la operazione giusta e mostra il 422 campo per campo', async () => {
    setup(user('boss'));
    vi.mocked(api.createOrgTeam)
      .mockRejectedValueOnce(
        new ApiError(
          { error: { code: 'validation_failed', message: 'Invalid', details: { fields: { name: 'must be a slug' } } } },
          422,
        ),
      )
      .mockResolvedValueOnce({ id: 't2', orgId: 'o1', name: 'frontend', createdAt: '2026-01-01T00:00:00Z' });
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('button', { name: /new team/i }));
    const form = screen.getByRole('form', { name: 'New team' });
    await u.type(within(form).getByLabelText('Team name'), 'Bad Name');
    await u.click(within(form).getByRole('button', { name: 'Create team' }));
    expect(await within(form).findByText('must be a slug')).toBeInTheDocument();
    await u.clear(within(form).getByLabelText('Team name'));
    await u.type(within(form).getByLabelText('Team name'), 'frontend');
    await u.click(within(form).getByRole('button', { name: 'Create team' }));
    await waitFor(() => expect(api.createOrgTeam).toHaveBeenLastCalledWith('acme', { name: 'frontend' }));
  });

  it('aggiunge un membro al team', async () => {
    setup(user('boss'));
    vi.mocked(api.saveTeamMember).mockResolvedValue(teamMember('boss', 'maintainer'));
    const u = userEvent.setup();
    renderPage();
    const form = await screen.findByRole('form', { name: 'Add team member' });
    await u.type(within(form).getByLabelText('Team member username'), 'boss');
    await u.selectOptions(within(form).getByLabelText('Team role'), 'maintainer');
    await u.click(within(form).getByRole('button', { name: /add to team/i }));
    await waitFor(() => expect(api.saveTeamMember).toHaveBeenCalledWith('acme', 'platform', 'boss', 'maintainer'));
  });

  it('501 dal backend: errore leggibile, la pagina non si rompe', async () => {
    vi.mocked(fetchSession).mockResolvedValue({ user: user('boss') } as never);
    vi.mocked(api.fetchOrganization).mockRejectedValue(
      new ApiError({ error: { code: 'not_implemented', message: 'Not implemented yet.' } }, 501),
    );
    vi.mocked(api.fetchOrgMembers).mockResolvedValue([]);
    vi.mocked(api.fetchTeams).mockResolvedValue([]);
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('Not implemented yet.');
  });
});
