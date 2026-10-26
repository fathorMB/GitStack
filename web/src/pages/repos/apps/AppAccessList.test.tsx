import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../../lib/http';
import { AppAccessPage } from './AppAccessPage';
import { makeApp, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchAppAccess: vi.fn(),
  saveAppAccess: vi.fn(),
  fetchAppAccessMembers: vi.fn(),
  addAppAccessMember: vi.fn(),
  removeAppAccessMember: vi.fn(),
  fetchAppAccessInvite: vi.fn(),
}));
vi.mock('../../../lib/issuesApi', () => ({ searchUsers: vi.fn() }));
vi.mock('../../../lib/orgsApi', () => ({ fetchTeams: vi.fn() }));

import * as api from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import type { AppRole } from '../../../lib/appsPermissions';
import * as issues from '../../../lib/issuesApi';

const T = 20000;
const access = { mode: 'login' as const, publicAppsEnabled: false, address: 'web-handbook-acme.apps.acme.it', certificate: 'instance_ca' as const };
const members = [
  { id: 'm1', kind: 'team' as const, name: 'acme/business', memberCount: 4, addedBy: { id: 'u1', username: 'mrossi', type: 'human' as const }, addedAt: '2026-10-26T10:00:00Z' },
  { id: 'm2', kind: 'user' as const, name: 'gverdi', email: 'giulia.verdi@retail.it', addedBy: { id: 'u1', username: 'mrossi', type: 'human' as const }, addedAt: '2026-10-26T10:00:00Z' },
];
const invite = {
  guideUrl: 'https://git.example/help/ca?app=x',
  fingerprintSha256: '4F:9A:21',
  address: 'https://web.apps.example',
  caMode: 'internal' as const,
  text: 'Hi, test web.\nSHA-256 4F:9A:21',
};

function mount(role: AppRole) {
  vi.mocked(api.fetchAppAccess).mockResolvedValue(access);
  render(
    <MemoryRouter>
      <AppAccessPage repo={repo} app={makeApp()} perms={appPermissions(role)} onChange={vi.fn()} />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchAppAccessMembers).mockResolvedValue(members);
});

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="access" (variante interna) e "testers".
describe('Access list (H6)', () => {
  it('admin: vede utenti e team, aggiunge e toglie', async () => {
    mount('admin');
    vi.mocked(issues.searchUsers).mockResolvedValue([{ username: 'lneri' } as never]);
    vi.mocked(api.addAppAccessMember).mockResolvedValue({ id: 'm3', kind: 'user', name: 'lneri', addedAt: '2026-10-26T11:00:00Z' });
    vi.mocked(api.removeAppAccessMember).mockResolvedValue(undefined);
    const list = await screen.findByRole('list', { name: 'Access list members' }, { timeout: T });
    expect(within(list).getByText('acme/business')).toBeInTheDocument();
    expect(within(list).getByText('team · 4 people')).toBeInTheDocument();
    expect(within(list).getByText('gverdi')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /Add people or team/ }));
    await userEvent.type(screen.getByLabelText('Username'), 'ln');
    await userEvent.click(await screen.findByRole('button', { name: 'lneri' }, { timeout: T }));
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(api.addAppAccessMember).toHaveBeenCalledWith('acme', 'handbook', 'web', 'user', 'lneri'), { timeout: T });
    expect(await within(screen.getByRole('list', { name: 'Access list members' })).findByText('lneri', undefined, { timeout: T })).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Remove gverdi' }));
    await waitFor(() => expect(api.removeAppAccessMember).toHaveBeenCalledWith('acme', 'handbook', 'web', 'm2'), { timeout: T });
    await waitFor(() => expect(screen.queryByText('gverdi')).not.toBeInTheDocument(), { timeout: T });
  }, T);

  it.each(['read', 'write'] as const)('%s: nessuna lista, nessuna chiamata, nessun comando né errore', async (role) => {
    mount(role);
    expect(await screen.findByText(/The access list is managed by admins/, undefined, { timeout: T })).toBeInTheDocument();
    expect(api.fetchAppAccessMembers).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /Add people or team/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Invite testers/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Remove/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  }, T);

  it('403 (permessi cambiati): errore nella sezione, la pagina resta su', async () => {
    vi.mocked(api.fetchAppAccessMembers).mockRejectedValue(new ApiError({ error: { code: 'forbidden', message: 'Not allowed' } }, 403));
    mount('admin');
    const section = await screen.findByRole('region', { name: 'Access list' }, { timeout: T });
    expect(await within(section).findByRole('alert', undefined, { timeout: T })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Internal · GitStack login' })).toBeChecked();
    expect(screen.getByText('Domains')).toBeInTheDocument();
  }, T);

  it('Invite testers: mostra il testo dell’API e lo copia', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    vi.mocked(api.fetchAppAccessInvite).mockResolvedValue(invite);
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: /Invite testers/ }, { timeout: T }));
    expect(await screen.findByTestId('invite-text', undefined, { timeout: T })).toHaveTextContent('SHA-256 4F:9A:21');
    expect(api.fetchAppAccessInvite).toHaveBeenCalledWith('acme', 'handbook', 'web');
    await userEvent.click(screen.getByRole('button', { name: /Copy text/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(invite.text), { timeout: T });
  }, T);
});
