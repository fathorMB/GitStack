import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { Repository } from '../../lib/reposApi';
import { ApiError } from '../../lib/http';
import { RepoSettingsSecretsPage } from './RepoSettingsSecretsPage';

vi.mock('./RepoHeader', () => ({ RepoHeader: () => null }));
vi.mock('../../lib/ciApi', () => ({
  fetchRepoSecrets: vi.fn(),
  fetchRepoVariables: vi.fn(),
  fetchOrgSecrets: vi.fn(),
  putRepoSecret: vi.fn(),
  putRepoVariable: vi.fn(),
  deleteRepoSecret: vi.fn(),
  deleteRepoVariable: vi.fn(),
  fetchRepoTokenPermissions: vi.fn(),
  putRepoTokenPermissions: vi.fn(),
}));
vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));

import {
  deleteRepoSecret,
  deleteRepoVariable,
  fetchOrgSecrets,
  fetchRepoSecrets,
  fetchRepoTokenPermissions,
  fetchRepoVariables,
  putRepoSecret,
  putRepoTokenPermissions,
} from '../../lib/ciApi';
import { fetchSession } from '../../lib/authApi';
import { fetchRepo } from '../../lib/reposApi';
import { fetchOrgMembers } from '../../lib/orgsApi';

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'GitStack',
    fullName: 'acme/GitStack',
    description: '',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: false,
    cloneUrls: { https: 'https://git.acme.local/acme/GitStack.git', ssh: 'ssh://git@git.acme.local:2222/acme/GitStack.git' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
    ...over,
  };
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/acme/GitStack/settings/secrets']}>
        <Routes>
          <Route path="/:owner/:repo/settings/secrets" element={<RepoSettingsSecretsPage />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

async function section(heading: string): Promise<HTMLElement> {
  const h = await screen.findByRole('heading', { name: heading });
  return h.closest('section') as HTMLElement;
}

describe('Settings · Secrets & variables (mockup B3)', { timeout: 30000 }, () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(fetchRepo).mockResolvedValue(repo());
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'mrossi', isAdmin: true } } as never);
    vi.mocked(fetchOrgMembers).mockResolvedValue([{ user: { username: 'mrossi' }, role: 'owner' }] as never);
    vi.mocked(fetchRepoSecrets).mockResolvedValue({ items: [{ name: 'GITHUB_MIRROR_TOKEN', updatedAt: '2026-10-16T00:00:00Z' }] });
    vi.mocked(fetchRepoVariables).mockResolvedValue({ items: [{ name: 'GO_VERSION', value: '1.23', updatedAt: '2026-10-16T00:00:00Z' }] });
    vi.mocked(fetchOrgSecrets).mockResolvedValue({ items: [{ name: 'S3_ARTIFACTS_KEY', updatedAt: '2026-10-01T00:00:00Z' }] });
    vi.mocked(fetchRepoTokenPermissions).mockResolvedValue({ contents: 'read', packages: 'read', actions: 'read' });
  });

  it('mostra i segreti del repo con nome e data, mai il valore', async () => {
    renderPage();
    const list = await section('Repository secrets');
    const row = within(list).getByText('GITHUB_MIRROR_TOKEN');
    expect(row.textContent).toBe('GITHUB_MIRROR_TOKEN');
    expect(within(list).getByText(/^updated /)).toBeInTheDocument();
    // Il contratto non ha nessun percorso che legga il valore: niente da rileggere.
    expect(within(list).queryByText('s3cr3t')).toBeNull();
    expect(within(list).getByText(/Values can't be read back/)).toBeInTheDocument();
  });

  it('i segreti dellorganizzazione sono ereditati e in sola lettura', async () => {
    renderPage();
    const list = await section('Organization secrets');
    expect(within(list).getByText('S3_ARTIFACTS_KEY')).toBeInTheDocument();
    expect(within(list).getByText(/inherited/)).toBeInTheDocument();
    expect(within(list).queryByRole('button', { name: 'New secret' })).toBeNull();
  });

  it('le variabili si rileggono, i segreti no: il PUT manda il valore e la lista non lo mostra', async () => {
    renderPage();
    expect(within(await section('Variables')).getByText('1.23')).toBeInTheDocument();

    const sectionEl = await section('Repository secrets');
    await userEvent.click(within(sectionEl).getByRole('button', { name: 'New secret' }));
    await userEvent.type(within(sectionEl).getByLabelText('Secret name'), 'COSIGN_KEY');
    await userEvent.type(within(sectionEl).getByLabelText('Value'), 's3cr3t-value');
    vi.mocked(fetchRepoSecrets).mockResolvedValue({ items: [{ name: 'COSIGN_KEY', updatedAt: '2026-10-19T00:00:00Z' }] });
    await userEvent.click(within(sectionEl).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(putRepoSecret).toHaveBeenCalledWith('acme', 'GitStack', { name: 'COSIGN_KEY', value: 's3cr3t-value' }));
    expect((within(sectionEl).getByLabelText('Value') as HTMLInputElement).value).toBe('');
    expect(within(sectionEl).queryByText('s3cr3t-value')).toBeNull();
    expect(within(sectionEl).getByText('COSIGN_KEY')).toBeInTheDocument();
  });

  it('non admin: niente segreti', async () => {
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'lbianchi', isAdmin: false } } as never);
    renderPage();
    expect(await screen.findByText('Settings are available only to repository admins.')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Repository secrets' })).toBeNull();
    expect(fetchRepoSecrets).not.toHaveBeenCalled();
  });

  it('il Delete di un segreto chiede conferma e poi chiama la DELETE', async () => {
    vi.mocked(deleteRepoSecret).mockResolvedValue(undefined);
    renderPage();
    const list = await section('Repository secrets');
    await userEvent.click(within(list).getByRole('button', { name: 'Delete secret GITHUB_MIRROR_TOKEN' }));
    // Prima della conferma non parte nessuna chiamata.
    expect(deleteRepoSecret).not.toHaveBeenCalled();
    vi.mocked(fetchRepoSecrets).mockResolvedValue({ items: [] });
    const dialog = await screen.findByRole('dialog');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(deleteRepoSecret).toHaveBeenCalledWith('acme', 'GitStack', 'GITHUB_MIRROR_TOKEN'));
    expect(await within(list).findByText('No secrets yet')).toBeInTheDocument();
  });

  it('annullare la conferma non elimina nulla; il Delete di una variabile usa la sua DELETE', async () => {
    vi.mocked(deleteRepoVariable).mockResolvedValue(undefined);
    renderPage();
    const vars = await section('Variables');
    await userEvent.click(within(vars).getByRole('button', { name: 'Delete variable GO_VERSION' }));
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    expect(deleteRepoVariable).not.toHaveBeenCalled();
    await userEvent.click(within(vars).getByRole('button', { name: 'Delete variable GO_VERSION' }));
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(deleteRepoVariable).toHaveBeenCalledWith('acme', 'GitStack', 'GO_VERSION'));
    expect(deleteRepoSecret).not.toHaveBeenCalled();
  });

  it('un errore della DELETE si vede', async () => {
    vi.mocked(deleteRepoSecret).mockRejectedValue(new ApiError({ error: { code: 'forbidden', message: 'Serve il permesso admin sul repo.' } }, 403));
    renderPage();
    const list = await section('Repository secrets');
    await userEvent.click(within(list).getByRole('button', { name: 'Delete secret GITHUB_MIRROR_TOKEN' }));
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete' }));
    expect(await screen.findByText(/permesso admin/)).toBeInTheDocument();
  });

  it('permessi del token del job: si leggono, si cambiano e si salvano con la PUT', async () => {
    vi.mocked(putRepoTokenPermissions).mockResolvedValue({ contents: 'write', packages: 'none', actions: 'read' });
    renderPage();
    const card = await section('Job token default permissions');
    expect((within(card).getByLabelText('Code') as HTMLSelectElement).value).toBe('read');
    const save = within(card).getByRole('button', { name: 'Save permissions' });
    expect(save).toBeDisabled();
    await userEvent.selectOptions(within(card).getByLabelText('Code'), 'write');
    await userEvent.selectOptions(within(card).getByLabelText('Packages'), 'none');
    await userEvent.click(save);
    await waitFor(() =>
      expect(putRepoTokenPermissions).toHaveBeenCalledWith('acme', 'GitStack', { contents: 'write', packages: 'none', actions: 'read' }),
    );
    await waitFor(() => expect(within(card).getByRole('button', { name: 'Save permissions' })).toBeDisabled());
    expect(within(card).getByText(/never gets more than the person or agent who started the run/)).toBeInTheDocument();
  });

  it('un errore del salvataggio dei permessi si vede e il tetto non cambia', async () => {
    vi.mocked(putRepoTokenPermissions).mockRejectedValue(new ApiError({ error: { code: 'archived', message: 'Il repo è archiviato: non accetta modifiche.' } }, 409));
    renderPage();
    const card = await section('Job token default permissions');
    await userEvent.selectOptions(within(card).getByLabelText('Actions'), 'write');
    await userEvent.click(within(card).getByRole('button', { name: 'Save permissions' }));
    expect(await within(card).findByText(/archiviato/)).toBeInTheDocument();
  });
});
