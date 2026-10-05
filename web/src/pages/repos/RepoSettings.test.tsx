import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import type { DeletedRepository, Repository } from '../../lib/reposApi';
import { daysLeft } from '../../lib/format';
import { DeletedReposPage } from './DeletedReposPage';
import { RepoPage } from './RepoPage';
import { RepoSettingsPage } from './RepoSettingsPage';

vi.mock('../../lib/reposApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/reposApi')>()),
  fetchRepo: vi.fn(),
  updateRepo: vi.fn(),
  deleteRepo: vi.fn(),
  fetchDeletedRepos: vi.fn(),
  restoreRepo: vi.fn(),
}));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import { fetchOrgMembers } from '../../lib/orgsApi';
import { deleteRepo, fetchDeletedRepos, fetchRepo, restoreRepo, updateRepo } from '../../lib/reposApi';

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api-gateway',
    fullName: 'acme/api-gateway',
    description: 'Single entry point',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: false,
    cloneUrls: { https: 'https://git.acme.local/acme/api-gateway.git', ssh: 'ssh://git@git.acme.local:2222/acme/api-gateway.git' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
    ...over,
  };
}

function session(username: string, isAdmin = false) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username, isAdmin } } as Awaited<ReturnType<typeof fetchSession>>);
}

function members(...list: [string, 'owner' | 'member'][]) {
  vi.mocked(fetchOrgMembers).mockResolvedValue(list.map(([u, role]) => ({ user: { username: u }, role })) as Awaited<ReturnType<typeof fetchOrgMembers>>);
}

function renderSettings() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/acme/api-gateway/settings']}>
        <Routes>
          <Route path="/:owner/:repo/settings" element={<RepoSettingsPage />} />
          <Route path="/repos" element={<p>Repos list</p>} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

function apiError(status: number, code: string, message: string, fields?: Record<string, string>) {
  return new ApiError({ error: { code, message, ...(fields ? { details: { fields } } : {}) } }, status);
}

describe('RepoSettingsPage', () => {
  beforeEach(() => {
    vi.mocked(fetchRepo).mockReset().mockResolvedValue(repo());
    vi.mocked(updateRepo).mockReset().mockImplementation(async (_o, _r, input) => repo(input));
    vi.mocked(deleteRepo).mockReset().mockResolvedValue(undefined);
    vi.mocked(fetchOrgMembers).mockReset();
    session('alice');
    members(['alice', 'owner']);
  });

  it('mostra le sezioni a un owner dell\'organizzazione', async () => {
    renderSettings();
    expect(await screen.findByRole('region', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Branch settings' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Danger zone' })).toBeInTheDocument();
    expect(screen.getByLabelText('Repository')).toBeDisabled();
    expect(screen.getByLabelText('Default branch')).toHaveValue('main');
    expect(screen.getByRole('checkbox', { name: /Protect the default branch/ })).toBeChecked();
  });

  it('non mostra le sezioni a chi non e\' admin del repo', async () => {
    members(['alice', 'member']);
    renderSettings();
    expect(await screen.findByText(/only to repository admins/)).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'General' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Danger zone' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('l\'admin dell\'installazione e il proprietario del repo personale vedono le sezioni', async () => {
    session('root', true);
    members();
    const view = renderSettings();
    expect(await screen.findByRole('region', { name: 'General' })).toBeInTheDocument();
    view.unmount();

    vi.mocked(fetchRepo).mockResolvedValue(repo({ owner: { type: 'user', name: 'bob' }, fullName: 'bob/api-gateway' }));
    session('bob');
    renderSettings();
    expect(await screen.findByRole('region', { name: 'General' })).toBeInTheDocument();
  });

  it('salva descrizione e visibilita', async () => {
    const user = userEvent.setup();
    renderSettings();
    const desc = await screen.findByLabelText('Description');
    await user.clear(desc);
    await user.type(desc, 'New text');
    await user.click(screen.getByRole('radio', { name: /Internal/ }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(updateRepo).toHaveBeenCalledWith('acme', 'api-gateway', { description: 'New text', visibility: 'internal' }));
  });

  it('salva branch principale e protezione', async () => {
    const user = userEvent.setup();
    renderSettings();
    const branch = await screen.findByLabelText('Default branch');
    await user.clear(branch);
    await user.type(branch, 'develop');
    await user.click(screen.getByRole('checkbox', { name: /Protect the default branch/ }));
    await user.click(screen.getByRole('button', { name: 'Save branch settings' }));
    await waitFor(() => expect(updateRepo).toHaveBeenCalledWith('acme', 'api-gateway', { defaultBranch: 'develop', protectDefaultBranch: false }));
  });

  it('mostra sul campo l\'errore 409/422 del branch', async () => {
    vi.mocked(updateRepo).mockRejectedValue(apiError(422, 'validation_failed', 'Invalid.', { defaultBranch: 'Branch "nope" does not exist.' }));
    const user = userEvent.setup();
    renderSettings();
    const branch = await screen.findByLabelText('Default branch');
    await user.clear(branch);
    await user.type(branch, 'nope');
    await user.click(screen.getByRole('button', { name: 'Save branch settings' }));
    expect(await screen.findByText('Branch "nope" does not exist.')).toBeInTheDocument();
    expect(screen.getByLabelText('Default branch')).toHaveAttribute('aria-invalid', 'true');
  });

  it('mostra il messaggio del server su un 409', async () => {
    vi.mocked(updateRepo).mockRejectedValue(apiError(409, 'conflict', 'Branch is not a branch of this repository.'));
    const user = userEvent.setup();
    renderSettings();
    await screen.findByLabelText('Default branch');
    await user.click(screen.getByRole('button', { name: 'Save branch settings' }));
    expect(await screen.findByText('Branch is not a branch of this repository.')).toBeInTheDocument();
  });

  it('archivia dopo la conferma', async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Archive' }));
    expect(updateRepo).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Archive repository' }));
    await waitFor(() => expect(updateRepo).toHaveBeenCalledWith('acme', 'api-gateway', { archived: true }));
    expect(await screen.findByText('Archived', { selector: '.badge' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Unarchive' })).toBeEnabled();
  });

  it('elimina con conferma che spiega i 7 giorni e torna all\'elenco', async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/7 days/)).toBeInTheDocument();
    expect(deleteRepo).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Delete repository' }));
    await waitFor(() => expect(deleteRepo).toHaveBeenCalledWith('acme', 'api-gateway'));
    expect(await screen.findByText('Repos list')).toBeInTheDocument();
  });

  it('annulla l\'eliminazione senza chiamare l\'API', async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    expect(deleteRepo).not.toHaveBeenCalled();
  });

  describe('repo archiviato', () => {
    beforeEach(() => {
      vi.mocked(fetchRepo).mockResolvedValue(repo({ archived: true, archivedAt: '2026-10-03T00:00:00Z' }));
    });

    it('mostra Archived e disabilita tutto tranne Unarchive', async () => {
      renderSettings();
      expect(await screen.findByText('Archived', { selector: '.badge' })).toBeInTheDocument();
      expect(screen.getByLabelText('Description')).toBeDisabled();
      expect(screen.getByRole('radio', { name: /Internal/ })).toBeDisabled();
      expect(screen.getByLabelText('Default branch')).toBeDisabled();
      expect(screen.getByRole('checkbox', { name: /Protect the default branch/ })).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Save branch settings' })).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
      expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Unarchive' })).toBeEnabled();
    });

    it('riattiva con archived:false da solo', async () => {
      const user = userEvent.setup();
      renderSettings();
      await user.click(await screen.findByRole('button', { name: 'Unarchive' }));
      await waitFor(() => expect(updateRepo).toHaveBeenCalledWith('acme', 'api-gateway', { archived: false }));
      await waitFor(() => expect(screen.getByLabelText('Description')).toBeEnabled());
      expect(screen.queryByText('Archived', { selector: '.badge' })).not.toBeInTheDocument();
    });
  });
});

describe('RepoPage: link alle impostazioni', () => {
  function renderPage() {
    return render(
      <ToastProvider>
        <MemoryRouter initialEntries={['/acme/api-gateway']}>
          <Routes>
            <Route path="/:owner/:repo" element={<RepoPage />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>,
    );
  }

  beforeEach(() => {
    vi.mocked(fetchRepo).mockReset().mockResolvedValue(repo());
    vi.mocked(fetchOrgMembers).mockReset();
  });

  it('c\'e\' per l\'admin', async () => {
    session('alice');
    members(['alice', 'owner']);
    renderPage();
    expect(await screen.findByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/acme/api-gateway/settings');
  });

  it('non c\'e\' senza permesso', async () => {
    session('alice');
    members(['alice', 'member']);
    renderPage();
    await screen.findByText(/acme \/ api-gateway/);
    await waitFor(() => expect(fetchOrgMembers).toHaveBeenCalled());
    expect(screen.queryByRole('link', { name: 'Settings' })).not.toBeInTheDocument();
  });
});

describe('DeletedReposPage', () => {
  const day = 24 * 60 * 60 * 1000;
  function deleted(id: string, name: string, purgeInDays: number): DeletedRepository {
    return {
      id,
      owner: { type: 'organization', name: 'acme' },
      name,
      deletedAt: new Date(Date.now() - (7 - purgeInDays) * day).toISOString(),
      purgeAt: new Date(Date.now() + purgeInDays * day - 1000).toISOString(),
    };
  }
  function renderDeleted() {
    return render(
      <ToastProvider>
        <MemoryRouter initialEntries={['/orgs/acme/deleted-repos']}>
          <Routes>
            <Route path="/orgs/:org/deleted-repos" element={<DeletedReposPage />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>,
    );
  }

  beforeEach(() => {
    vi.mocked(fetchDeletedRepos).mockReset().mockResolvedValue([deleted('d1', 'experiment-grpc', 6), deleted('d2', 'old-docs', 1)]);
    vi.mocked(restoreRepo).mockReset().mockImplementation(async () => repo({ name: 'old-docs', fullName: 'acme/old-docs' }));
  });

  it('elenca i repo eliminati con i giorni rimasti, per l\'owner', async () => {
    renderDeleted();
    const list = await screen.findByRole('list', { name: 'Deleted repositories' });
    expect(fetchDeletedRepos).toHaveBeenCalledWith('acme');
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText('acme/experiment-grpc')).toBeInTheDocument();
    expect(within(rows[0]).getByText('6 days')).toBeInTheDocument();
    expect(within(rows[1]).getByText('1 day')).toBeInTheDocument();
  });

  it('ripristina un repo e lo toglie dall\'elenco', async () => {
    const user = userEvent.setup();
    renderDeleted();
    await user.click(await screen.findByRole('button', { name: 'Restore acme/old-docs' }));
    await waitFor(() => expect(restoreRepo).toHaveBeenCalledWith('d2'));
    await waitFor(() => expect(screen.queryByText('acme/old-docs')).not.toBeInTheDocument());
    expect(screen.getByText('acme/experiment-grpc')).toBeInTheDocument();
  });

  it('mostra l\'errore del ripristino e lascia la riga', async () => {
    vi.mocked(restoreRepo).mockRejectedValue(apiError(404, 'not_found', 'Gone.'));
    const user = userEvent.setup();
    renderDeleted();
    await user.click(await screen.findByRole('button', { name: 'Restore acme/old-docs' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.getByText('acme/old-docs')).toBeInTheDocument();
  });

  it('mostra lo stato vuoto', async () => {
    vi.mocked(fetchDeletedRepos).mockResolvedValue([]);
    renderDeleted();
    expect(await screen.findByText('No deleted repositories')).toBeInTheDocument();
  });

  it('daysLeft arrotonda per eccesso e non scende sotto 0', () => {
    const now = Date.parse('2026-10-05T00:00:00Z');
    expect(daysLeft('2026-10-11T12:00:00Z', now)).toBe(7);
    expect(daysLeft('2026-10-06T00:00:00Z', now)).toBe(1);
    expect(daysLeft('2026-10-01T00:00:00Z', now)).toBe(0);
  });
});
