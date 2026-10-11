import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import type { RepoMirror, RepoMirrorRun } from '../../lib/mirrorsApi';
import { RepoMirrorsSection } from './RepoMirrorsSection';

vi.mock('../../lib/mirrorsApi', () => ({
  fetchMirrors: vi.fn(),
  fetchMirrorRuns: vi.fn(),
  addMirror: vi.fn(),
  editMirror: vi.fn(),
  removeMirror: vi.fn(),
  syncMirror: vi.fn(),
}));

import { addMirror, editMirror, fetchMirrorRuns, fetchMirrors, removeMirror, syncMirror } from '../../lib/mirrorsApi';

function mirror(over: Partial<RepoMirror> = {}): RepoMirror {
  return {
    id: 'm1',
    url: 'https://github.com/acme/api.git',
    username: 'mirror-bot',
    hasToken: true,
    enabled: true,
    state: 'in_sync',
    attempts: 0,
    nextAttemptAt: null,
    lastAttemptAt: '2026-10-07T10:00:00Z',
    lastSuccessAt: '2026-10-07T10:00:00Z',
    lastError: null,
    lastPushed: { 'refs/heads/main': 'abcdef0123456789abcdef0123456789abcdef01' },
    createdAt: '2026-10-06T00:00:00Z',
    updatedAt: '2026-10-07T10:00:00Z',
    ...over,
  };
}

function renderSection(readOnly = false) {
  return render(
    <ToastProvider>
      <RepoMirrorsSection owner="acme" repo="api" readOnly={readOnly} />
    </ToastProvider>,
  );
}

describe('RepoMirrorsSection', () => {
  beforeEach(() => {
    vi.mocked(fetchMirrors).mockReset().mockResolvedValue([mirror()]);
    vi.mocked(fetchMirrorRuns).mockReset().mockResolvedValue([]);
    vi.mocked(addMirror).mockReset();
    vi.mocked(editMirror).mockReset();
    vi.mocked(removeMirror).mockReset().mockResolvedValue(undefined);
    vi.mocked(syncMirror).mockReset();
  });

  it('elenca i mirror con stato, ultimo push riuscito e SHA', async () => {
    renderSection();
    const row = await screen.findByTestId('mirror-row');
    expect(within(row).getByText('https://github.com/acme/api.git')).toBeInTheDocument();
    expect(within(row).getByText('In sync')).toBeInTheDocument();
    expect(within(row).getByText(/main@abcdef0/)).toBeInTheDocument();
    expect(within(row).getByText(/last successful push/)).toBeInTheDocument();
  });

  it.each([
    ['in_sync', 'In sync'],
    ['syncing', 'Syncing'],
    ['pending', 'Queued'],
    ['error', 'Error'],
    ['diverged', 'Stopped: diverged'],
  ] as const)('rende lo stato %s con testo e colore', async (state, label) => {
    vi.mocked(fetchMirrors).mockResolvedValue([mirror({ state })]);
    renderSection();
    const badge = await screen.findByText(label);
    expect(badge.closest('[data-state]')).toHaveAttribute('data-state', state);
    expect((badge.closest('[data-state]') as HTMLElement).style.color).toMatch(/^var\(--/);
  });

  it('mostra l\'ultimo errore leggibile e "never pushed"', async () => {
    vi.mocked(fetchMirrors).mockResolvedValue([mirror({ state: 'error', lastSuccessAt: null, lastPushed: {}, lastError: 'authentication failed' })]);
    renderSection();
    expect(await screen.findByText('authentication failed')).toBeInTheDocument();
    expect(screen.getByText(/never pushed/)).toBeInTheDocument();
  });

  it('mostra il log delle ultime esecuzioni', async () => {
    const runs: RepoMirrorRun[] = [
      {
        id: 'r1',
        startedAt: '2026-10-07T10:00:00Z',
        finishedAt: '2026-10-07T10:00:01Z',
        durationMs: 420,
        outcome: 'diverged',
        attempt: 2,
        error: 'non-fast-forward',
        refs: [{ ref: 'refs/heads/main', status: 'rejected', reason: 'non-fast-forward' }],
      },
    ];
    vi.mocked(fetchMirrorRuns).mockResolvedValue(runs);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /Recent runs of/ }));
    expect(await screen.findByText('Diverged')).toBeInTheDocument();
    expect(screen.getByText(/refs\/heads\/main · rejected · non-fast-forward/)).toBeInTheDocument();
    expect(fetchMirrorRuns).toHaveBeenCalledWith('acme', 'api', 'm1');
  });

  it('«Sync now» chiama l\'API e aggiorna lo stato', async () => {
    vi.mocked(syncMirror).mockResolvedValue(mirror({ state: 'pending' }));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Sync now' }));
    expect(syncMirror).toHaveBeenCalledWith('acme', 'api', 'm1');
    expect(await screen.findByText('Queued')).toBeInTheDocument();
  });

  it('«Sync now» e\' spento su un mirror disabilitato', async () => {
    vi.mocked(fetchMirrors).mockResolvedValue([mirror({ enabled: false })]);
    renderSection();
    expect(await screen.findByRole('button', { name: 'Sync now' })).toBeDisabled();
    expect(screen.getByText('Disabled')).toBeInTheDocument();
  });

  it('disattiva un mirror', async () => {
    vi.mocked(editMirror).mockResolvedValue(mirror({ enabled: false }));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Disable' }));
    expect(editMirror).toHaveBeenCalledWith('acme', 'api', 'm1', { enabled: false });
    expect(await screen.findByRole('button', { name: 'Enable' })).toBeInTheDocument();
  });

  it('aggiunge un mirror con URL, utente e token', async () => {
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    vi.mocked(addMirror).mockResolvedValue(mirror({ id: 'm2', url: 'https://home.lan/acme/api.git', state: 'pending' }));
    const user = userEvent.setup();
    renderSection();
    expect(await screen.findByText(/No mirrors yet/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    await user.type(within(form).getByLabelText('Remote URL'), 'https://home.lan/acme/api.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    await user.type(within(form).getByLabelText('Token'), 's3cret');
    await user.click(within(form).getByRole('button', { name: 'Add mirror' }));
    expect(addMirror).toHaveBeenCalledWith('acme', 'api', { url: 'https://home.lan/acme/api.git', username: 'bot', token: 's3cret' });
    expect(await screen.findByText('https://home.lan/acme/api.git')).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'Add mirror' })).not.toBeInTheDocument();
  });

  it('mostra l\'errore di validazione sul campo', async () => {
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    vi.mocked(addMirror).mockRejectedValue(
      new ApiError({ error: { code: 'validation_failed', message: 'Invalid', details: { fields: { url: 'url must be https' } } } }, 422),
    );
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    await user.type(screen.getByLabelText('Remote URL'), 'http://x');
    await user.type(screen.getByLabelText('Username'), 'u');
    await user.type(screen.getByLabelText('Token'), 't');
    await user.click(within(screen.getByRole('form', { name: 'Add mirror' })).getByRole('button', { name: 'Add mirror' }));
    expect(await screen.findByText('url must be https')).toBeInTheDocument();
  });

  it('in modifica il token non si rilegge e si invia solo se sostituito', async () => {
    vi.mocked(editMirror).mockResolvedValue(mirror({ username: 'other' }));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /Edit mirror/ }));
    const form = screen.getByRole('form', { name: 'Edit mirror' });
    expect(within(form).getByLabelText('Token')).toHaveValue('');
    const username = within(form).getByLabelText('Username');
    await user.clear(username);
    await user.type(username, 'other');
    await user.click(within(form).getByRole('button', { name: 'Save mirror' }));
    expect(editMirror).toHaveBeenCalledWith('acme', 'api', 'm1', { username: 'other' });
  });

  it('elimina con conferma', async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /Delete mirror https/ }));
    expect(removeMirror).not.toHaveBeenCalled();
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete mirror' }));
    await waitFor(() => expect(removeMirror).toHaveBeenCalledWith('acme', 'api', 'm1'));
    await waitFor(() => expect(screen.queryByTestId('mirror-row')).not.toBeInTheDocument());
  });

  it('su un repo archiviato i comandi sono spenti', async () => {
    renderSection(true);
    expect(await screen.findByRole('button', { name: 'Sync now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add mirror' })).toBeDisabled();
  });
});
