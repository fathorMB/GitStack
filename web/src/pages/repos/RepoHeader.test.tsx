import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { Repository } from '../../lib/reposApi';
import { RepoHeader } from './RepoHeader';
import type { RepoHeaderMode } from './RepoHeader';
import { RepoPage } from './RepoPage';
import { RepoSettingsPage } from './RepoSettingsPage';
import { RepoWebhooksPage } from './RepoWebhooksPage';

vi.mock('../../lib/reposApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/reposApi')>()),
  fetchRepo: vi.fn(),
}));
vi.mock('../../lib/mirrorsApi', () => ({
  fetchMirrors: vi.fn().mockResolvedValue([]),
  fetchMirrorRuns: vi.fn().mockResolvedValue([]),
  addMirror: vi.fn(),
  editMirror: vi.fn(),
  removeMirror: vi.fn(),
  syncMirror: vi.fn(),
}));
vi.mock('../../lib/webhooksApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/webhooksApi')>()),
  fetchRepoWebhooks: vi.fn().mockResolvedValue({ items: [], page: 1, perPage: 50, total: 0 }),
}));
vi.mock('../../lib/issuesApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/issuesApi')>()),
  countIssues: vi.fn().mockResolvedValue(3),
}));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('./RepoWatchSelect', () => ({ RepoWatchSelect: () => <span>watch</span> }));

import { fetchSession } from '../../lib/authApi';
import { fetchOrgMembers } from '../../lib/orgsApi';
import { fetchRepo } from '../../lib/reposApi';

function repo(): Repository {
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
  };
}

function asRole(role: 'owner' | 'member') {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'alice', isAdmin: false } } as Awaited<ReturnType<typeof fetchSession>>);
  vi.mocked(fetchOrgMembers).mockResolvedValue([{ user: { username: 'alice' }, role }] as Awaited<ReturnType<typeof fetchOrgMembers>>);
}

function tab(name: string) {
  return within(screen.getByRole('navigation', { name: 'Repository sections' })).getByRole('link', { name: new RegExp(name) });
}

beforeEach(() => {
  vi.mocked(fetchRepo).mockReset().mockResolvedValue(repo());
});

describe('RepoHeader: schede', () => {
  function renderHeader(mode: RepoHeaderMode, admin: boolean) {
    return render(
      <MemoryRouter>
        <RepoHeader repo={repo()} mode={mode} admin={admin} />
      </MemoryRouter>,
    );
  }

  it('la scheda Settings c\'e\' per l\'admin e manca per gli altri', () => {
    const view = renderHeader('tree', true);
    expect(tab('Settings')).toHaveAttribute('href', '/acme/api-gateway/settings');
    view.unmount();
    renderHeader('tree', false);
    expect(within(screen.getByRole('navigation', { name: 'Repository sections' })).queryByRole('link', { name: /Settings/ })).not.toBeInTheDocument();
  });

  it('Settings non sta nella riga del titolo', () => {
    const { container } = renderHeader('tree', true);
    const title = container.querySelector('.repo-title') as HTMLElement;
    expect(within(title).queryByRole('link', { name: /Settings/ })).not.toBeInTheDocument();
  });

  it.each(['tree', 'commits', 'commit', 'tags', 'search', 'blob', 'blame'] as RepoHeaderMode[])('Code e\' attiva su %s', (mode) => {
    renderHeader(mode, true);
    expect(tab('Code')).toHaveClass('active');
    expect(tab('Issues')).not.toHaveClass('active');
    expect(tab('Settings')).not.toHaveClass('active');
  });

  it.each(['issues', 'issue', 'newissue', 'labels', 'milestones'] as RepoHeaderMode[])('Issues e\' attiva su %s', (mode) => {
    renderHeader(mode, true);
    expect(tab('Issues')).toHaveClass('active');
    expect(tab('Code')).not.toHaveClass('active');
    expect(tab('Settings')).not.toHaveClass('active');
  });

  it.each(['settings', 'webhooks'] as RepoHeaderMode[])('Settings e\' attiva su %s e Code e Issues no', (mode) => {
    renderHeader(mode, true);
    expect(tab('Settings')).toHaveClass('active');
    expect(tab('Code')).not.toHaveClass('active');
    expect(tab('Issues')).not.toHaveClass('active');
  });
});

describe('Schede del repo nelle pagine', () => {
  function renderAt(path: string) {
    return render(
      <ToastProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/:owner/:repo" element={<RepoPage />} />
            <Route path="/:owner/:repo/settings" element={<RepoSettingsPage />} />
            <Route path="/:owner/:repo/webhooks" element={<RepoWebhooksPage />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>,
    );
  }

  it('RepoPage: Settings per l\'admin, non attiva e fuori dalla riga del titolo', async () => {
    asRole('owner');
    const { container } = renderAt('/acme/api-gateway');
    await waitFor(() => expect(tab('Settings')).toHaveAttribute('href', '/acme/api-gateway/settings'));
    expect(tab('Settings')).not.toHaveClass('active');
    expect(tab('Code')).toHaveClass('active');
    expect(within(container.querySelector('.repo-title') as HTMLElement).queryByRole('link', { name: /Settings/ })).not.toBeInTheDocument();
  });

  it('RepoPage: senza permesso la scheda manca', async () => {
    asRole('member');
    renderAt('/acme/api-gateway');
    await screen.findByRole('heading', { name: /acme \/ api-gateway/ });
    await waitFor(() => expect(fetchOrgMembers).toHaveBeenCalled());
    expect(screen.queryByRole('link', { name: /Settings/ })).not.toBeInTheDocument();
  });

  it('/settings (con i mirror): intestazione con Settings attiva, Code e Issues no', async () => {
    asRole('owner');
    renderAt('/acme/api-gateway/settings');
    expect(await screen.findByRole('region', { name: 'General' })).toBeInTheDocument();
    const mirrors = await screen.findAllByRole('region', { name: /mirror/i });
    expect(mirrors.length).toBeGreaterThanOrEqual(2);
    expect(tab('Settings')).toHaveClass('active');
    expect(tab('Code')).not.toHaveClass('active');
    expect(tab('Issues')).not.toHaveClass('active');
    expect(fetchRepo).toHaveBeenCalledTimes(1);
  });

  it('/webhooks: intestazione con Settings attiva, Code e Issues no', async () => {
    asRole('owner');
    renderAt('/acme/api-gateway/webhooks');
    expect(await screen.findByRole('navigation', { name: 'Repository sections' })).toBeInTheDocument();
    expect(tab('Settings')).toHaveClass('active');
    expect(tab('Code')).not.toHaveClass('active');
    expect(tab('Issues')).not.toHaveClass('active');
  });
});
