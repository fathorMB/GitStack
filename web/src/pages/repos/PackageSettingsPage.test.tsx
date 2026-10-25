import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { PackageSettings } from '../../lib/packagesApi';
import type { Repository } from '../../lib/reposApi';
import { PackageSettingsPage } from './PackageSettingsPage';

vi.mock('../../lib/packagesApi', () => ({ fetchPackageSettings: vi.fn(), savePackageSettings: vi.fn() }));
vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));


vi.mock('../../lib/issuesApi', () => ({ countIssues: vi.fn() }));
vi.mock('../../lib/pullsApi', () => ({ countPulls: vi.fn() }));
vi.mock('./RepoWatchSelect', () => ({ RepoWatchSelect: () => null }));

import { fetchSession } from '../../lib/authApi';
import { fetchPackageSettings, savePackageSettings } from '../../lib/packagesApi';
import { fetchOrgMembers } from '../../lib/orgsApi';
import { countIssues } from '../../lib/issuesApi';
import { countPulls } from '../../lib/pullsApi';
import { fetchRepo } from '../../lib/reposApi';

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api',
    fullName: 'acme/api',
    description: '',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: false,
    cloneUrls: { https: 'https://git.acme.local/acme/api.git' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
    ...over,
  };
}

const settings = (over: Partial<PackageSettings> = {}): PackageSettings => ({
  protectedTagPatterns: ['latest', 'v*'],
  deleteUntaggedAfterDays: 7,
  includeInBackup: false,
  ...over,
});

function session(isAdmin: boolean) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: isAdmin ? 'owner' : 'mrossi', isAdmin } } as Awaited<ReturnType<typeof fetchSession>>);
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/acme/api/settings/packages']}>
        <Routes>
          <Route path="/:owner/:repo/settings/packages" element={<PackageSettingsPage />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('PackageSettingsPage (mockup B6)', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(countIssues).mockResolvedValue(0);
    vi.mocked(countPulls).mockResolvedValue(0);
    vi.mocked(fetchRepo).mockResolvedValue(repo());
    vi.mocked(fetchPackageSettings).mockResolvedValue(settings());
    vi.mocked(fetchOrgMembers).mockResolvedValue([{ user: { username: 'owner' }, role: 'owner' }] as never);
  });

  it('mostra tag protetti, pulizia e backup con i valori salvati', async () => {
    session(true);
    renderPage();
    const protectedTags = await screen.findByRole('region', { name: 'Protected tags' }, { timeout: 5000 });
    expect(within(protectedTags).getByRole('list', { name: 'Protected tag patterns' })).toHaveTextContent('latest');
    const cleanup = screen.getByRole('region', { name: 'Cleanup' });
    expect(within(cleanup).getByText(/removed automatically after 7 days/)).toBeInTheDocument();
    expect(within(cleanup).queryByLabelText('Delete untagged images after (days)')).not.toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Backups' })).getByRole('checkbox')).not.toBeChecked();
  });

  it('non admin: niente impostazioni', async () => {
    session(false);
    renderPage();
    expect(await screen.findByText('Package settings are available only to repository admins.')).toBeInTheDocument();
    expect(fetchPackageSettings).not.toHaveBeenCalled();
  });

  it('salva le modifiche con il PATCH completo', async () => {
    session(true);
    vi.mocked(savePackageSettings).mockResolvedValue(settings({ protectedTagPatterns: ['latest', 'v*', 'stable'], includeInBackup: true, keepLastTags: 5 }));
    renderPage();
    const protectedTags = await screen.findByRole('region', { name: 'Protected tags' }, { timeout: 5000 });
    await userEvent.type(within(protectedTags).getByLabelText('Tag pattern'), 'stable{Enter}');
    await userEvent.type(screen.getByLabelText('Keep only the last tags per image'), '5');
    await userEvent.click(within(screen.getByRole('region', { name: 'Backups' })).getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Save package settings' }));
    expect(savePackageSettings).toHaveBeenCalledWith('acme', 'api', {
      protectedTagPatterns: ['latest', 'v*', 'stable'],
      includeInBackup: true,
      keepLastTags: 5,
    });
    expect(await screen.findByText('Package settings saved.')).toBeInTheDocument();
  });

  it('rifiuta giorni non validi senza chiamare il server', async () => {
    session(true);
    renderPage();
    const days = await screen.findByLabelText('Delete sha-* tags after (days)', undefined, { timeout: 5000 });
    await userEvent.type(days, '0');
    await userEvent.click(screen.getByRole('button', { name: 'Save package settings' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/whole number of days/);
    expect(savePackageSettings).not.toHaveBeenCalled();
  });

  it('repo archiviato: sola lettura', async () => {
    session(true);
    vi.mocked(fetchRepo).mockResolvedValue(repo({ archived: true }));
    renderPage();
    expect(await screen.findByText(/package settings are read-only/, undefined, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save package settings' })).toBeDisabled();
  });
});
