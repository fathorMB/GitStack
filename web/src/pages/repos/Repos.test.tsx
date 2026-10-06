import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { validateRepoName } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { NewRepoPage } from './NewRepoPage';
import { RepoPage } from './RepoPage';
import { ReposPage } from './ReposPage';

vi.mock('../../lib/reposApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/reposApi')>()),
  fetchRepos: vi.fn(),
  fetchRepo: vi.fn(),
  createRepo: vi.fn(),
}));
vi.mock('../../lib/orgsApi', () => ({ fetchOrganizations: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import { fetchOrganizations } from '../../lib/orgsApi';
import { createRepo, fetchRepo, fetchRepos } from '../../lib/reposApi';

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

function renderAt(path: string) {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/repos" element={<ReposPage />} />
          <Route path="/new" element={<NewRepoPage />} />
          <Route path="/:owner/:repo" element={<RepoPage />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('ReposPage', () => {
  beforeEach(() => {
    vi.mocked(fetchRepos)
      .mockReset()
      .mockResolvedValue([
        repo(),
        repo({ id: 'r2', name: 'identity', fullName: 'acme/identity', visibility: 'internal', description: 'Users' }),
        repo({ id: 'r3', name: 'legacy', fullName: 'acme/legacy', archived: true, archivedAt: '2026-08-01T00:00:00Z' }),
      ]);
  });

  it('elenca i repo con visibilita e badge Archived', async () => {
    renderAt('/repos');
    const list = await screen.findByRole('list', { name: 'Repositories' });
    expect(within(list).getAllByRole('listitem')).toHaveLength(3);
    expect(within(list).getByRole('link', { name: 'acme/api-gateway' })).toHaveAttribute('href', '/acme/api-gateway');
    expect(within(list).getByText('Internal')).toBeInTheDocument();
    expect(within(list).getByText('Archived', { selector: '.badge' })).toBeInTheDocument();
  });

  it('filtra per visibilita, archiviati e ricerca', async () => {
    const u = userEvent.setup();
    renderAt('/repos');
    await screen.findByRole('list', { name: 'Repositories' });
    await u.click(screen.getByRole('button', { name: 'Internal' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    await u.click(screen.getByRole('button', { name: 'Archived' }));
    expect(screen.getByRole('link', { name: 'acme/legacy' })).toBeInTheDocument();
    await u.click(screen.getByRole('button', { name: 'All' }));
    await u.type(screen.getByRole('searchbox', { name: 'Find a repository' }), 'zzz');
    expect(screen.getByText('No repositories match')).toBeInTheDocument();
  });

  it('senza repo mostra lo stato vuoto con il link a New repository', async () => {
    vi.mocked(fetchRepos).mockResolvedValue([]);
    renderAt('/repos');
    expect(await screen.findByText('No repositories yet')).toBeInTheDocument();
  });
});

describe('validateRepoName (R11)', () => {
  it.each([
    ['billing-service', ''],
    ['_x.y-1', ''],
    ['', 'Enter a name.'],
    ['GitStack', ''],
    ['GITSTACK', ''],
    ['Repo.GIT', '.git'],
    ['.Repo', 'Start'],
    ['a'.repeat(101), 'at most 100'],
    ['a b', 'Use only'],
    ['x.git', '.git'],
  ])('%s', (name, expected) => {
    const msg = validateRepoName(name);
    if (expected === '') expect(msg).toBe('');
    else expect(msg).toContain(expected);
  });
});

describe('NewRepoPage', () => {
  beforeEach(() => {
    vi.mocked(fetchSession).mockReset().mockResolvedValue({ user: { username: 'mrossi' } } as never);
    vi.mocked(fetchOrganizations)
      .mockReset()
      .mockResolvedValue([{ id: 'o1', name: 'acme', createdAt: '2026-01-01T00:00:00Z' }] as never);
    vi.mocked(createRepo).mockReset();
  });

  async function form() {
    renderAt('/new');
    const f = await screen.findByRole('form', { name: 'New repository' });
    await waitFor(() => expect(within(f).getByRole('combobox', { name: 'Owner' })).toHaveTextContent('mrossi'));
    return f;
  }

  it('default: Private, opzioni spente, owner ben in vista con la nota R3', async () => {
    const f = await form();
    expect(within(f).getByRole('radio', { name: /Private/ })).toBeChecked();
    expect(within(f).getByRole('radio', { name: /Internal/ })).not.toBeChecked();
    for (const label of ['Add a README', 'Add .gitignore', 'Add a license']) {
      expect(within(f).getByRole('checkbox', { name: label })).not.toBeChecked();
    }
    expect(within(f).getByText(/can.t be changed later/)).toBeInTheDocument();
  });

  it('valida il nome nel form e non invia', async () => {
    const u = userEvent.setup();
    const f = await form();
    await u.type(within(f).getByLabelText('Repository name'), 'x.git');
    expect(await within(f).findByText(/cannot end with/)).toBeInTheDocument();
    await u.click(within(f).getByRole('button', { name: 'Create repository' }));
    expect(createRepo).not.toHaveBeenCalled();
  });

  it('crea con i default: private, readme false, nessun modello', async () => {
    vi.mocked(createRepo).mockResolvedValue(repo({ id: 'n', owner: { type: 'user', name: 'mrossi' }, name: 'demo', fullName: 'mrossi/demo', empty: true }));
    vi.mocked(fetchRepo).mockResolvedValue(repo({ owner: { type: 'user', name: 'mrossi' }, name: 'demo', fullName: 'mrossi/demo', empty: true }));
    const u = userEvent.setup();
    const f = await form();
    await u.type(within(f).getByLabelText('Repository name'), 'demo');
    await u.click(within(f).getByRole('button', { name: 'Create repository' }));
    await waitFor(() => expect(createRepo).toHaveBeenCalledWith({ owner: 'mrossi', name: 'demo', visibility: 'private', readme: false, defaultLabels: true }));
    expect(await screen.findByRole('region', { name: 'Quick setup' })).toBeInTheDocument();
  });

  it('Add default labels e attiva di default e si puo disattivare (I5)', async () => {
    vi.mocked(createRepo).mockRejectedValue(new ApiError({ error: { code: 'conflict', message: 'Name taken.' } }, 409));
    const u = userEvent.setup();
    const f = await form();
    const box = within(f).getByRole('checkbox', { name: 'Add default labels' });
    expect(box).toBeChecked();
    await u.click(box);
    await u.type(within(f).getByLabelText('Repository name'), 'demo');
    await u.click(within(f).getByRole('button', { name: 'Create repository' }));
    await waitFor(() => expect(createRepo).toHaveBeenCalledWith({ owner: 'mrossi', name: 'demo', visibility: 'private', readme: false, defaultLabels: false }));
  });

  it('Internal e opzioni attive finiscono nel corpo, con il primo modello', async () => {
    vi.mocked(createRepo).mockRejectedValue(new ApiError({ error: { code: 'conflict', message: 'Name taken.' } }, 409));
    const u = userEvent.setup();
    const f = await form();
    await u.type(within(f).getByLabelText('Repository name'), 'demo');
    await u.click(within(f).getByRole('radio', { name: /Internal/ }));
    await u.click(within(f).getByRole('checkbox', { name: 'Add a README' }));
    await u.click(within(f).getByRole('checkbox', { name: 'Add .gitignore' }));
    await u.click(within(f).getByRole('button', { name: 'Create repository' }));
    await waitFor(() =>
      expect(createRepo).toHaveBeenCalledWith({ owner: 'mrossi', name: 'demo', visibility: 'internal', readme: true, defaultLabels: true, gitignoreTemplate: 'go' }),
    );
    expect(await within(f).findByText('Name taken.')).toBeInTheDocument();
  });
});

describe('RepoPage', () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  beforeEach(() => {
    writeText.mockClear();
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    vi.mocked(fetchRepo).mockReset();
  });

  it('repo vuoto: quick setup con indirizzi dalla risposta e copia', async () => {
    vi.mocked(fetchRepo).mockResolvedValue(repo({ empty: true }));
    const u = userEvent.setup();
    renderAt('/acme/api-gateway');
    const box = await screen.findByRole('region', { name: 'Quick setup' });
    expect(within(box).getByLabelText('Clone URL')).toHaveValue('https://git.acme.local/acme/api-gateway.git');
    await u.click(within(box).getByRole('button', { name: 'SSH' }));
    expect(within(box).getByLabelText('Clone URL')).toHaveValue('ssh://git@git.acme.local:2222/acme/api-gateway.git');
    await u.click(within(box).getByRole('button', { name: 'Copy' }));
    expect(await within(box).findByText('Copied')).toBeInTheDocument();
    expect(box.textContent).toContain('gs repo clone acme/api-gateway');
  });

  it('R11: un indirizzo con altre maiuscole porta alla forma canonica del nome', async () => {
    vi.mocked(fetchRepo).mockResolvedValue(
      repo({ empty: true, name: 'GitStack', fullName: 'acme/GitStack' }),
    );
    renderAt('/acme/gitstack');
    expect(await screen.findByRole('heading', { level: 1, name: /GitStack/ })).toBeInTheDocument();
    await waitFor(() => expect(fetchRepo).toHaveBeenLastCalledWith('acme', 'GitStack'));
  });

  it('repo non vuoto: niente quick setup, parte il browser del codice', async () => {
    vi.mocked(fetchRepo).mockResolvedValue(repo());
    renderAt('/acme/api-gateway');
    expect(await screen.findByText(/Loading code|Unexpected response|Could not reach/)).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Quick setup' })).not.toBeInTheDocument();
  });
});
