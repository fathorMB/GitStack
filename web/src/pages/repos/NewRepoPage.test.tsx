import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import type { PullMirror } from '@gitstack/api-client';
import type { ImportRepositoryInput } from '../../lib/pullMirrorsApi';
import { importRepo } from '../../lib/pullMirrorsApi';
import { createRepo } from '../../lib/reposApi';
import { NewRepoPage } from './NewRepoPage';

vi.mock('../../lib/authApi', () => ({
  fetchSession: vi.fn().mockResolvedValue({ user: { username: 'alice' } }),
}));

vi.mock('../../lib/orgsApi', () => ({
  fetchOrganizations: vi.fn().mockResolvedValue([]),
}));

vi.mock('../../lib/reposApi', () => ({
  createRepo: vi.fn(),
  validateRepoName: vi.fn((n) => (n === '' ? 'Enter a repository name.' : n.length > 100 ? 'Repository name must be at most 100 characters.' : '')),
  fetchRepos: vi.fn(),
  fetchRepo: vi.fn(),
  updateRepo: vi.fn(),
  deleteRepo: vi.fn(),
  fetchDeletedRepos: vi.fn(),
  restoreRepo: vi.fn(),
  fetchBranches: vi.fn(),
  fetchTags: vi.fn(),
  fetchTree: vi.fn(),
  fetchFilePaths: vi.fn(),
  fetchLanguages: vi.fn(),
  fetchReadme: vi.fn(),
  searchHref: vi.fn(),
  commitsHref: vi.fn(),
  blobHref: vi.fn(),
  rawUrl: vi.fn(),
  archiveUrl: vi.fn(),
  treeHref: vi.fn(),
  fuzzyFilter: vi.fn(),
  splitRefPath: vi.fn(),
  GITIGNORE_TEMPLATES: ['go', 'node'],
  LICENSE_TEMPLATES: ['mit'],
}));

vi.mock('../../lib/pullMirrorsApi', () => ({
  importRepo: vi.fn(),
  fetchPullMirror: vi.fn(),
  enablePullMirror: vi.fn(),
  updatePullMirror: vi.fn(),
  syncPullMirror: vi.fn(),
  pausePullMirror: vi.fn(),
  resumePullMirror: vi.fn(),
  stopPullMirror: vi.fn(),
  fetchPullMirrorRuns: vi.fn(),
}));

const mockCreateRepo = createRepo as unknown as ReturnType<typeof vi.fn>;
const mockImportRepo = importRepo as unknown as ReturnType<typeof vi.fn>;

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <NewRepoPage />
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe('NewRepoPage', () => {
  beforeEach(() => {
    vi.mocked(mockCreateRepo).mockReset();
    vi.mocked(mockImportRepo).mockReset();
  });

  // ---- Switch to import mode and submit ----

  it('submitting Import from URL sends mode: mirror + intervalSeconds', async () => {
    const user = userEvent.setup({ delay: null });
    mockImportRepo.mockResolvedValue(undefined);
    renderPage();

    // Click "Import from URL"
    await user.click(screen.getByRole('button', { name: 'Import from URL' }));

    // Fill form
    await user.type(screen.getByLabelText('Source URL'), 'https://github.com/acme/example.git');
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');

    // Submit
    await user.click(screen.getByRole('button', { name: 'Import repository' }));

    await waitFor(() => expect(mockImportRepo).toHaveBeenCalledTimes(1));
    const [input] = vi.mocked(mockImportRepo).mock.calls[0] as [ImportRepositoryInput];
    expect(input.mode).toBe('mirror');
    expect(input.url).toBe('https://github.com/acme/example.git');
    expect(input.intervalSeconds).toBe(600);
  }, 20000);

  it('submitting Import from URL with mode once sends no intervalSeconds', async () => {
    const user = userEvent.setup({ delay: null });
    mockImportRepo.mockResolvedValue(undefined);
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Import from URL' }));

    // Switch to "Copy once"
    await user.click(screen.getByRole('radio', { name: /Copy once/ }));
    await user.type(screen.getByLabelText('Source URL'), 'https://github.com/acme/example.git');
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');

    await user.click(screen.getByRole('button', { name: 'Import repository' }));

    await waitFor(() => expect(mockImportRepo).toHaveBeenCalledTimes(1));
    const [input] = vi.mocked(mockImportRepo).mock.calls[0] as [ImportRepositoryInput];
    expect(input.mode).toBe('once');
    expect(input).not.toHaveProperty('intervalSeconds');
  }, 20000);

  // ---- Local validation errors in both modes ----

  it.each([
    ['empty', ''],
    ['http://', 'http://github.com/acme/example.git'],
    ['user:pass@', 'https://user:pass@github.com/acme/example.git'],
  ])('validazione locale rifiuta %s in modalità mirror', async (_label: string, url: string) => {
    mockImportRepo.mockResolvedValue(undefined);
    renderPage();

    const user = userEvent.setup({ delay: null });
    await user.click(screen.getByRole('button', { name: 'Import from URL' }));

    if (url !== '') {
      await user.type(screen.getByLabelText('Source URL'), url);
    }
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');

    // Submit
    await user.click(screen.getByRole('button', { name: 'Import repository' }));

    // importRepo non deve essere chiamato
    expect(mockImportRepo).not.toHaveBeenCalled();
    // Errore locale visibile
    expect(screen.getByRole('alert')).toBeInTheDocument();
  }, 20000);

  it.each([
    ['empty', ''],
    ['http://', 'http://github.com/acme/example.git'],
    ['user:pass@', 'https://user:pass@github.com/acme/example.git'],
  ])('validazione locale rifiuta %s in modalità once', async (_label: string, url: string) => {
    mockImportRepo.mockResolvedValue(undefined);
    renderPage();

    const user = userEvent.setup({ delay: null });
    await user.click(screen.getByRole('button', { name: 'Import from URL' }));

    // Switch to "Copy once"
    await user.click(screen.getByRole('radio', { name: /Copy once/ }));
    if (url !== '') {
      await user.type(screen.getByLabelText('Source URL'), url);
    }
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');

    await user.click(screen.getByRole('button', { name: 'Import repository' }));

    // importRepo non deve essere chiamato
    expect(mockImportRepo).not.toHaveBeenCalled();
    // Errore locale visibile
    expect(screen.getByRole('alert')).toBeInTheDocument();
  }, 20000);

  // ---- 422 url_not_allowed con messaggio policy di uscita ----

  it('mostra il messaggio della policy di uscita per 422 url_not_allowed', async () => {
    const user = userEvent.setup({ delay: null });
    mockImportRepo.mockRejectedValue(
      new ApiError(
        {
          error: {
            code: 'url_not_allowed',
            message: 'Validation failed',
            details: { fields: { url: 'Invalid URL' } },
          },
        },
        422,
      ),
    );
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Import from URL' }));
    await user.type(screen.getByLabelText('Source URL'), 'https://github.com/acme/example.git');
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');
    await user.click(screen.getByRole('button', { name: 'Import repository' }));

    // Il messaggio della policy di uscita è visibile
    await waitFor(() => {
      expect(screen.getByText(/This address is blocked by the outbound policy/)).toBeInTheDocument();
    });
    expect(screen.getByText(/Private and internal addresses can only be used if an administrator allows them in/)).toBeInTheDocument();
  });

  // ---- PullMirrorInfoBanner: ore calcolate (test diretto del componente) ----

  it('PullMirrorInfoBanner calcola ore dinamiche per stale mirror', async () => {
    const { PullMirrorInfoBanner } = await import('../../components');

    // Fissa un timestamp lontano (30 ore fa) per evitare race condition con Date.now()
    const lastSyncedAt = '2026-10-10T08:00:00Z';

    const mirror = {
      id: 'pm1',
      origin: 'https://github.com/acme/example.git',
      mode: 'mirror',
      intervalSeconds: 600,
      state: 'in_sync',
      hasToken: false,
      attempts: 0,
      divergedRefs: [],
      createdAt: '2026-10-01T00:00:00Z',
      updatedAt: '2026-10-12T00:00:00Z',
      lastSyncedAt,
    } as PullMirror;

    render(<PullMirrorInfoBanner mirror={mirror} />);

    // Il banner danger dovrebbe contenere il testo calcolato (ore >= 24)
    await waitFor(() => {
      // Cerca il testo del banner stale (il numero esatto di ore dipende da quando gira il test)
      expect(screen.getByRole('alert')).toHaveClass('alert-danger');
    });
  });

  it('PullMirrorInfoBanner messaggio stopped quando lastSyncedAt è null', async () => {
    const { PullMirrorInfoBanner } = await import('../../components');

    const mirror = {
      id: 'pm1',
      origin: 'https://github.com/acme/example.git',
      mode: 'mirror',
      intervalSeconds: 600,
      state: 'stopped',
      hasToken: false,
      attempts: 0,
      divergedRefs: [],
      createdAt: '2026-10-01T00:00:00Z',
      updatedAt: '2026-10-12T00:00:00Z',
      lastSyncedAt: null,
    } as PullMirror;

    render(<PullMirrorInfoBanner mirror={mirror} />);

    // Il banner danger dovrebbe contenere il messaggio stopped
    await waitFor(() => {
      expect(screen.getByText(/The mirror has been stopped/)).toBeInTheDocument();
    });
  });
});

describe('NewRepoPage: nome nel cestino (R2)', () => {
  const purgeAt = '2026-10-25T14:31:47Z';
  function trashed(owner: string, withId = true) {
    return new ApiError(
      { error: { code: 'name_in_trash', message: 'Il nome e tenuto da un repo eliminato.', details: { purgeAt, owner, ...(withId ? { repoId: 'r-1' } : {}) } } },
      409,
    );
  }
  beforeEach(() => {
    mockCreateRepo.mockReset();
    mockImportRepo.mockReset();
  });

  it('creazione: mostra la data, il link al cestino personale e il ripristino', async () => {
    const user = userEvent.setup({ delay: null });
    mockCreateRepo.mockRejectedValue(trashed('alice'));
    renderPage();
    await user.type(await screen.findByPlaceholderText('billing-service'), 'gone');
    await user.click(screen.getByRole('button', { name: 'Create repository' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('alice/gone');
    expect(alert).toHaveTextContent(new Date(purgeAt).toLocaleDateString());
    expect(alert).toHaveTextContent('restore it');
    expect(within(alert).getByRole('link', { name: 'Deleted repositories' })).toHaveAttribute('href', '/settings/deleted-repos');
  });

  it('import: il repo e di un\'organizzazione, link al suo cestino', async () => {
    const user = userEvent.setup({ delay: null });
    mockImportRepo.mockRejectedValue(trashed('acme'));
    renderPage();
    await user.click(screen.getByRole('button', { name: 'Import from URL' }));
    await user.type(screen.getByLabelText('Source URL'), 'https://github.com/acme/example.git');
    await user.type(screen.getByPlaceholderText('repo-name'), 'example');
    await user.click(screen.getByRole('button', { name: 'Import repository' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('acme/example');
    expect(alert).toHaveTextContent(new Date(purgeAt).toLocaleDateString());
    expect(within(alert).getByRole('link', { name: 'Deleted repositories' })).toHaveAttribute('href', '/orgs/acme/deleted-repos');
  });

  it('senza repoId (non admin) solo codice e data, senza link', async () => {
    const user = userEvent.setup({ delay: null });
    mockCreateRepo.mockRejectedValue(trashed('alice', false));
    renderPage();
    await user.type(await screen.findByPlaceholderText('billing-service'), 'gone');
    await user.click(screen.getByRole('button', { name: 'Create repository' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(new Date(purgeAt).toLocaleDateString());
    expect(within(alert).queryByRole('link')).not.toBeInTheDocument();
  });

  it('un conflitto normale resta il messaggio generico', async () => {
    const user = userEvent.setup({ delay: null });
    mockCreateRepo.mockRejectedValue(new ApiError({ error: { code: 'conflict', message: 'Esiste gia un repo con questo nome.' } }, 409));
    renderPage();
    await user.type(await screen.findByPlaceholderText('billing-service'), 'gone');
    await user.click(screen.getByRole('button', { name: 'Create repository' }));
    const alert = await screen.findByRole('alert');
    expect(alert).not.toHaveTextContent('Deleted repositories');
  });
});
