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

vi.mock('../../lib/pullMirrorsApi', () => ({
  fetchPullMirror: vi.fn(),
}));

import { addMirror, editMirror, fetchMirrorRuns, fetchMirrors, removeMirror, syncMirror } from '../../lib/mirrorsApi';
import { fetchPullMirror } from '../../lib/pullMirrorsApi';
import type { PullMirror } from '../../lib/pullMirrorsApi';

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

function pullMirror(over: Partial<PullMirror> = {}): PullMirror {
  return {
    id: 'pm1',
    origin: 'https://github.com/acme/origin.git',
    mode: 'mirror',
    intervalSeconds: 600,
    state: 'in_sync',
    hasToken: true,
    attempts: 0,
    nextSyncAt: null,
    divergedRefs: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-12T10:00:00Z',
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
    vi.mocked(fetchPullMirror).mockReset().mockResolvedValue(null);
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
    await user.click(within(form).getByLabelText('Remote URL')); await user.paste('https://home.lan/acme/api.git');
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

  // P5: blocco push mirror verso stessa origine del pull mirror
  // Solo per stati non stopped: pending, syncing, in_sync, paused → blocco.
  // stopped → permesso con messaggio tappa 2 (V7).
  // Il blocco "Add mirror" per push mirror esistenti è stato rimosso (punto 5d).

  it('blocca il form se il pull mirror e\' in_sync verso l\'origine', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'in_sync' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    await waitFor(() => expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument(), { timeout: 3000 });
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
  });

  it('blocca il form se il pull mirror e\' in pausa verso l\'origine', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'paused' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    await waitFor(() => expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument(), { timeout: 3000 });
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
  });

  it('blocca il form se il pull mirror e\' pending verso l\'origine (MP6)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'pending' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    await user.type(within(form).getByLabelText('Token'), 't');
    // Il pulsante resta spento per il blocco MP6 (pending è attivo).
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  it('blocca il form se il pull mirror e\' syncing verso l\'origine (MP6)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'syncing' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    // Token richiesto per nuovo mirror → pulsante spento.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Inserisci token: il pulsante resta spento per il blocco MP6.
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 't');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  it('blocca il form se il pull mirror e\' error verso l\'origine (MP6)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'error' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    // Token richiesto per nuovo mirror → pulsante spento.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Inserisci token: il pulsante resta spento per il blocco MP6.
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 't');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  // Tappa 2 di V7: stopped → ammesso con messaggio.
  it('permesso form se il pull mirror e\' stopped (tappa 2 V7)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({
      origin: 'https://github.com/acme/origin.git',
      state: 'stopped',
      stoppedAt: '2026-10-11T10:00:00Z',
    }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    // Il messaggio tappa 2 sostituisce l'avviso di conflitto.
    await waitFor(() => expect(within(form).getByText(/The pull mirror was stopped/)).toBeInTheDocument(), { timeout: 3000 });
    // Il token è obbligatorio per un nuovo mirror.
    const tokenInput = within(form).getByLabelText('Token');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    await user.type(tokenInput, 's3cret');
    await waitFor(() => expect(within(form).getByRole('button', { name: 'Add mirror' })).not.toBeDisabled(), { timeout: 3000 });
  });

  it('con pull mirror in paused il pulsante submit è spento anche con token pieno', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'paused' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    // Il pulsante submit resta spento per il blocco pull mirror (paused ≠ stopped).
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  // Punto 5: con pull mirror attivo (in_sync) il pulsante Add mirror apre il form
  // ma il submit resta disabilitato e c'è l'avviso.
  it('con pull mirror attivo il submit del form è disabilitato anche se esiste un push mirror', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'in_sync' }));
    vi.mocked(fetchMirrors).mockResolvedValue([
      mirror({ id: 'm1', url: 'https://github.com/acme/origin.git', username: 'bot', hasToken: true, state: 'in_sync' }),
    ]);
    const user = userEvent.setup();
    renderSection();
    // Il pulsante Add mirror apre il form.
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    // Scriviamo l'URL dell'origine del pull mirror.
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin');
    // Il submit resta disabilitato per il blocco pull mirror.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  // Punto 5: con pull mirror stopped (tappa 2) e stessa origine il submit è abilitato.
  it('con pull mirror stopped (stessa origine) il submit del form è abilitato (tappa 2)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'stopped' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    // Il pulsante submit è abilitato (stopped → tappa 2).
    expect(within(form).getByRole('button', { name: 'Add mirror' })).not.toBeDisabled();
    // Messaggio informativo tappa 2 presente.
    expect(within(form).getByText(/the home of the code/)).toBeInTheDocument();
    // Nessun avviso di blocco.
    expect(within(form).queryByText(/This is the source of the pull mirror/)).not.toBeInTheDocument();
  });

  // Punto 4: con pull mirror in pending/syncing/error il pulsante è spento.
  it('con pull mirror in pending il pulsante Add mirror del form è spento', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'pending' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  it('con pull mirror in syncing il pulsante Add mirror del form è spento', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'syncing' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  it('con pull mirror in error il pulsante Add mirror del form è spento', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'error' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://github.com/acme/origin.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  // Punto 6: token obbligatorio per un nuovo push mirror.
  it('nuovo push mirror richiede token (punto 6)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(null);
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    const usernameInput = within(form).getByLabelText('Username');
    await user.click(urlInput); await user.paste('https://github.com/acme/dest.git');
    await user.type(usernameInput, 'bot');
    // Token vuoto: pulsante spento.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Inserisci token: pulsante abilitato.
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    await waitFor(() => expect(within(form).getByRole('button', { name: 'Add mirror' })).not.toBeDisabled(), { timeout: 3000 });
  });

  it('modifica push mirror non richiede token (lascia vuoto per mantenere)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(null);
    vi.mocked(fetchMirrors).mockResolvedValue([
      mirror({ id: 'm1', url: 'https://github.com/acme/dest.git', username: 'bot', hasToken: true, state: 'in_sync' }),
    ]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /Edit/ }));
    const form = screen.getByRole('form', { name: 'Edit mirror' });
    // Il pulsante è abilitato anche senza token perché si sta modificando un mirror esistente.
    expect(within(form).getByRole('button', { name: 'Save mirror' })).not.toBeDisabled();
  });

  it('senza pull mirror il pulsante Add mirror e\' attivo', async () => {
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    renderSection();
    const addBtn = await screen.findByRole('button', { name: 'Add mirror' });
    expect(addBtn).not.toBeDisabled();
  });

  // Punto 1 (rework 3): pull mirror in_sync + destinazione diversa → nessun avviso, form attivo.
  it('con pull mirror in_sync e destinazione diversa l\'avviso non c\'è e il form è attivo', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'in_sync' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    await user.click(urlInput); await user.paste('https://example.com/other.git');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    // Nessun avviso di blocco.
    expect(within(form).queryByText(/This is the source of the pull mirror/)).not.toBeInTheDocument();
    // Nessuna info tappa 2.
    expect(within(form).queryByText(/the home of the code/)).not.toBeInTheDocument();
    // Pulsante attivo.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).not.toBeDisabled();
  });

  // Punto 1 (rework 3): stessa origine con .git mancante → bloccato.
  it('stessa origine senza .git è bloccata (normalizzazione URL)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'in_sync' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    // L'utente scrive senza .git.
    await user.click(urlInput); await user.paste('https://github.com/acme/origin');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    // Pulsante bloccato.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

  // Punto 1 (rework 3): stessa origine con trailing slash diverso → bloccato.
  it('stessa origine con slash finale diverso è bloccata (normalizzazione URL)', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ origin: 'https://github.com/acme/origin.git', state: 'in_sync' }));
    vi.mocked(fetchMirrors).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Add mirror' }));
    const form = screen.getByRole('form', { name: 'Add mirror' });
    const urlInput = within(form).getByLabelText('Remote URL');
    // L'utente scrive con slash finale ma senza .git.
    await user.click(urlInput); await user.paste('https://github.com/acme/origin/');
    await user.type(within(form).getByLabelText('Username'), 'bot');
    const tokenInput = within(form).getByLabelText('Token');
    await user.type(tokenInput, 's3cret');
    // Pulsante bloccato.
    expect(within(form).getByRole('button', { name: 'Add mirror' })).toBeDisabled();
    // Avviso di blocco presente.
    expect(within(form).getByText(/This is the source of the pull mirror/)).toBeInTheDocument();
  });

});
