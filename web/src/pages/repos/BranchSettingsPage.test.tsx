import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { BranchSettings } from '../../lib/branchSettingsApi';
import type { Repository } from '../../lib/reposApi';
import { BranchSettingsPage } from './BranchSettingsPage';

vi.mock('../../lib/branchSettingsApi', () => ({ fetchBranchSettings: vi.fn(), saveBranchSettings: vi.fn() }));
vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn(), updateRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));

import { fetchBranchSettings, saveBranchSettings } from '../../lib/branchSettingsApi';
import { fetchSession } from '../../lib/authApi';
import { fetchRepo, updateRepo } from '../../lib/reposApi';
import { fetchOrgMembers } from '../../lib/orgsApi';

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api-gateway',
    fullName: 'acme/api-gateway',
    description: '',
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

const settings = (over: Partial<BranchSettings> = {}): BranchSettings => ({
  allowMergeCommit: true,
  allowSquashMerge: true,
  defaultMergeMethod: 'squash',
  deleteBranchOnMerge: false,
  requirePullRequest: true,
  requiredApprovals: 2,
  requiredStatusContexts: ['test'],
  dismissStaleApprovals: true,
  ...over,
});

function session(username: string, isAdmin: boolean) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username, isAdmin } } as Awaited<ReturnType<typeof fetchSession>>);
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/acme/api-gateway/settings/branches']}>
        <Routes>
          <Route path="/:owner/:repo/settings/branches" element={<BranchSettingsPage />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('BranchSettingsPage (mockup 28)', () => {
  beforeEach(() => {
    // Radix Select in jsdom: servono questi due metodi (come in PullMirrorSection.test.tsx).
    Object.defineProperty(EventTarget.prototype, 'hasPointerCapture', { value: vi.fn().mockReturnValue(false), writable: true });
    Object.defineProperty(EventTarget.prototype, 'releasePointerCapture', { value: vi.fn(), writable: true });
    HTMLElement.prototype.scrollIntoView = vi.fn();
    vi.resetAllMocks();
    vi.mocked(fetchRepo).mockResolvedValue(repo());
    vi.mocked(fetchBranchSettings).mockResolvedValue(settings());
    vi.mocked(fetchOrgMembers).mockResolvedValue([{ user: { username: 'owner' }, role: 'owner' }] as never);
  });

  it('mostra le regole e i merge all admin, con i valori salvati', async () => {
    session('owner', true);
    renderPage();

    const rules = await screen.findByRole('region', { name: 'Rules' });
    expect(within(rules).getByRole('checkbox', { name: 'Require a pull request before merging' })).toBeChecked();
    expect(within(rules).getByRole('combobox', { name: 'Required approvals from people' })).toHaveTextContent('2');
    expect(within(rules).getByRole('list', { name: 'Required status contexts' })).toHaveTextContent('test');

    const merging = screen.getByRole('region', { name: 'Merging pull requests' });
    expect(within(merging).getByRole('checkbox', { name: 'Allow squash merging' })).toBeChecked();
    expect(within(merging).getByRole('checkbox', { name: 'Allow merge commits' })).toBeChecked();
    expect(within(merging).getByRole('checkbox', { name: 'Automatically delete head branches' })).not.toBeChecked();
  });

  it('non admin: niente impostazioni', async () => {
    session('mrossi', false);
    renderPage();
    expect(await screen.findByText('Branches & merge settings are available only to repository admins.')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Rules' })).toBeNull();
    expect(fetchBranchSettings).not.toHaveBeenCalled();
  });

  it('il salvataggio manda il PATCH con i campi invariati e mostra il toast', async () => {
    session('owner', true);
    vi.mocked(saveBranchSettings).mockResolvedValue(settings({ requiredApprovals: 3 }));
    renderPage();
    const rules = await screen.findByRole('region', { name: 'Rules' });

    await userEvent.click(within(rules).getByRole('combobox', { name: 'Required approvals from people' }));
    await userEvent.click(await screen.findByRole('option', { name: '3' }));
    await userEvent.click(within(rules).getByRole('button', { name: 'Save rules' }));

    await waitFor(() =>
      expect(saveBranchSettings).toHaveBeenCalledWith('acme', 'api-gateway', {
        allowMergeCommit: true,
        allowSquashMerge: true,
        defaultMergeMethod: 'squash',
        deleteBranchOnMerge: false,
        requirePullRequest: true,
        requiredApprovals: 3,
        requiredStatusContexts: ['test'],
      }),
    );
    expect(updateRepo).not.toHaveBeenCalled();
    expect(await screen.findByText('Rules saved.')).toBeInTheDocument();
  });

  it('aggiunge e rimuove un contesto obbligatorio', async () => {
    session('owner', true);
    vi.mocked(saveBranchSettings).mockResolvedValue(settings({ requiredStatusContexts: ['test', 'lint'] }));
    renderPage();
    const rules = await screen.findByRole('region', { name: 'Rules' });

    const input = within(rules).getByPlaceholderText('Add a context, e.g. lint');
    await userEvent.type(input, 'lint{Enter}');
    expect(within(rules).getByRole('list', { name: 'Required status contexts' })).toHaveTextContent('lint');

    await userEvent.click(within(rules).getByRole('button', { name: 'Remove test' }));
    await userEvent.click(within(rules).getByRole('button', { name: 'Save rules' }));
    await waitFor(() => expect(saveBranchSettings).toHaveBeenCalledWith('acme', 'api-gateway', expect.objectContaining({ requiredStatusContexts: ['lint'] })));
  });

  it('non lascia spegnere entrambi i metodi di merge', async () => {
    session('owner', true);
    renderPage();
    const merging = await screen.findByRole('region', { name: 'Merging pull requests' });

    await userEvent.click(within(merging).getByRole('checkbox', { name: 'Allow squash merging' }));
    await userEvent.click(within(merging).getByRole('checkbox', { name: 'Allow merge commits' }));
    await userEvent.click(within(merging).getByRole('button', { name: 'Save' }));

    expect(await within(merging).findByRole('alert')).toHaveTextContent('At least one merge method stays enabled.');
    expect(saveBranchSettings).not.toHaveBeenCalled();
  });

  it('su un repo archiviato i campi sono in sola lettura', async () => {
    session('owner', true);
    vi.mocked(fetchRepo).mockResolvedValue(repo({ archived: true }));
    renderPage();
    const rules = await screen.findByRole('region', { name: 'Rules' });
    expect(within(rules).getByRole('checkbox', { name: 'Require a pull request before merging' })).toBeDisabled();
    expect(within(rules).getByRole('button', { name: 'Save rules' })).toBeDisabled();
  });
});
