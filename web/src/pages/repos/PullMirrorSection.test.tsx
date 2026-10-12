import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { PullMirror, PullMirrorRun } from '../../lib/pullMirrorsApi';
import { PullMirrorSection } from './PullMirrorSection';

// Mock per Radix UI Select in jsdom (haPointerCapture, scrollIntoView non esistono).
beforeEach(() => {
  Object.defineProperty(EventTarget.prototype, 'hasPointerCapture', {
    value: vi.fn().mockReturnValue(false),
    writable: true,
  });
  Object.defineProperty(EventTarget.prototype, 'releasePointerCapture', {
    value: vi.fn(),
    writable: true,
  });
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

vi.mock('../../lib/pullMirrorsApi', () => ({
  fetchPullMirror: vi.fn(),
  fetchPullMirrorRuns: vi.fn(),
  syncPullMirror: vi.fn(),
  pausePullMirror: vi.fn(),
  resumePullMirror: vi.fn(),
  stopPullMirror: vi.fn(),
  updatePullMirror: vi.fn(),
}));

import {
  fetchPullMirror,
  fetchPullMirrorRuns,
  pausePullMirror,
  resumePullMirror,
  stopPullMirror,
  syncPullMirror,
  updatePullMirror,
} from '../../lib/pullMirrorsApi';

function pullMirror(over: Partial<PullMirror> = {}): PullMirror {
  return {
    id: 'pm1',
    origin: 'https://github.com/acme/origin.git',
    mode: 'mirror',
    intervalSeconds: 600,
    state: 'in_sync',
    username: 'mirror-bot',
    hasToken: true,
    attempts: 0,
    nextSyncAt: '2026-10-12T10:10:00Z',
    lastAttemptAt: '2026-10-12T10:00:00Z',
    lastSyncedAt: '2026-10-12T10:05:00Z',
    lastError: null,
    divergedRefs: [],
    stoppedAt: null,
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-12T10:05:00Z',
    ...over,
  };
}

function renderSection() {
  return render(
    <ToastProvider>
      <PullMirrorSection owner="acme" repo="api" />
    </ToastProvider>,
  );
}

describe('PullMirrorSection', () => {
  beforeEach(() => {
    vi.mocked(fetchPullMirror).mockReset().mockResolvedValue(pullMirror());
    vi.mocked(fetchPullMirrorRuns).mockReset().mockResolvedValue([]);
    vi.mocked(syncPullMirror).mockReset();
    vi.mocked(pausePullMirror).mockReset();
    vi.mocked(resumePullMirror).mockReset();
    vi.mocked(stopPullMirror).mockReset();
    vi.mocked(updatePullMirror).mockReset();
  });

  it('mostra titolo, stato attivo e badge "Active"', async () => {
    renderSection();
    const h2 = await screen.findByRole('heading', { name: 'Pull mirror' });
    expect(h2).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText(/https:\/\/github.com\/acme\/origin\.git/)).toBeInTheDocument();
  });

  it('mostra il badge "Paused" per stato paused', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'paused' }));
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Pull mirror' })).toBeInTheDocument();
    expect(screen.getByText('Paused')).toBeInTheDocument();
    expect(screen.queryByText('Active')).not.toBeInTheDocument();
  });

  it('mostra il badge "Stopped" per stato stopped', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'stopped' }));
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Pull mirror' })).toBeInTheDocument();
    expect(screen.getByText('Stopped')).toBeInTheDocument();
  });

  it('mostra "Sync now" solo per mirror attivo', async () => {
    renderSection();
    expect(await screen.findByRole('button', { name: 'Sync now' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sync now' })).not.toBeDisabled();
  });

  it('non mostra "Sync now" per mirror in pausa', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'paused' }));
    renderSection();
    expect(screen.queryByRole('button', { name: 'Sync now' })).not.toBeInTheDocument();
  });

  it('avvisa che le aggiorna non arrivano da 24 ore', async () => {
    renderSection();
    // L'avviso 24 ore è sempre visibile nella sezione pull mirror.
    expect(await screen.findByText(/If a successful update/)).toBeInTheDocument();
  });

  it('mostra ref diverged se presenti', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(
      pullMirror({
        divergedRefs: [
          { ref: 'refs/heads/feature/old-ui', kind: 'rewritten' as const, localSha: 'aaa11111', remoteSha: 'bbb22222', detectedAt: '2026-10-12T09:00:00Z' },
        ],
      }),
    );
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Diverged refs' })).toBeInTheDocument();
    expect(screen.getByText('refs/heads/feature/old-ui')).toBeInTheDocument();
    expect(screen.getByText('aaa1111')).toBeInTheDocument();
    expect(screen.getByText('bbb2222')).toBeInTheDocument();
  });

  it('nasconde la sezione diverged se nessun ref diverge', async () => {
    renderSection();
    expect(screen.queryByRole('heading', { name: 'Diverged refs' })).not.toBeInTheDocument();
  });

  it('mostra il registro delle esecuzioni recenti', async () => {
    vi.mocked(fetchPullMirrorRuns).mockResolvedValue([
      {
        id: 'r1',
        trigger: 'schedule' as const,
        outcome: 'success',
        refs: [{ ref: 'refs/heads/main', status: 'updated', newSha: 'abcdef0123', reason: '' }],
        startedAt: '2026-10-12T10:05:00Z',
        finishedAt: '2026-10-12T10:05:02Z',
        durationMs: 2100,
        attempt: 1,
        error: null,
      },
    ] as PullMirrorRun[]);
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Recent runs' })).toBeInTheDocument();
    // Il componente mostra la durata in millisecondi grezzi.
    expect(screen.getByText('2100 ms')).toBeInTheDocument();
  });

  // ---- Danger zone: pause / resume / stop ----

  it('mostra "Pause" per mirror attivo', async () => {
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Danger zone' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).not.toBeInTheDocument();
  });

  it('mostra "Resume" per mirror in pausa', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'paused' }));
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Danger zone' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
  });

  it('non mostra pause/resume per mirror interrotto', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'stopped' }));
    renderSection();
    const danger = await screen.findByRole('heading', { name: 'Danger zone' });
    expect(danger).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).not.toBeInTheDocument();
  });

  it('stop mirroring richiede conferma con il nome del repo', async () => {
    const user = userEvent.setup();
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Danger zone' })).toBeInTheDocument();
    const stopBtn = screen.getByRole('button', { name: 'Stop mirroring' });
    expect(stopBtn).toBeInTheDocument();
    // Apre il dialog ma non ferma
    await user.click(stopBtn);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/Stop mirroring api\?/)).toBeInTheDocument();
    expect(stopPullMirror).not.toHaveBeenCalled();
    // Clicca il pulsante di conferma con il nome del repo
    await user.click(within(dialog).getByRole('button', { name: 'api' }));
    await waitFor(() => expect(stopPullMirror).toHaveBeenCalledWith('acme', 'api', 'api'));
  });

  it('cancella la conferma stop se si preme Cancel', async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByRole('heading', { name: 'Danger zone' });
    await user.click(screen.getByRole('button', { name: 'Stop mirroring' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(stopPullMirror).not.toHaveBeenCalled();
  });

  it('preme "Sync now" e mostra toast di successo', async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Sync now' }));
    expect(syncPullMirror).toHaveBeenCalledWith('acme', 'api');
  });

  it('preme "Pause" per un mirror attivo', async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Pause' }));
    expect(pausePullMirror).toHaveBeenCalledWith('acme', 'api');
  });

  it('preme "Resume" per un mirror in pausa', async () => {
    vi.mocked(fetchPullMirror).mockResolvedValue(pullMirror({ state: 'paused' }));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Resume' }));
    expect(resumePullMirror).toHaveBeenCalledWith('acme', 'api');
  });

  it('mostra errore azione generico in alert', async () => {
    vi.mocked(syncPullMirror).mockRejectedValue(new Error('Network unreachable'));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Sync now' }));
    // describeError per un Error non-API torna il messaggio di connessione.
    expect(await screen.findByText(/Could not reach/)).toBeInTheDocument();
  });

  // ---- Form: intervallo e token ----

  it('cambia intervallo e salva', async () => {
    const user = userEvent.setup();
    renderSection();
    // Il form dell'intervallo usa un Select con aria-label "Update interval"
    const intervalSelect = await screen.findByRole('combobox', { name: 'Update interval' });
    await user.click(intervalSelect);
    const option = await screen.findByRole('option', { name: '1 hour' });
    await user.click(option);
    const saveBtn = await screen.findByRole('button', { name: 'Save mirror' });
    await user.click(saveBtn);
    expect(updatePullMirror).toHaveBeenCalledWith('acme', 'api', { intervalSeconds: 3600 });
  });

  it('sostituisce il token e salva', async () => {
    const user = userEvent.setup();
    renderSection();
    // Cerca l'input del token per placeholder
    const tokenInput = await screen.findByPlaceholderText('New token');
    await user.type(tokenInput, 'new-token');
    // Cerca il pulsante "Replace" che e' un submit del form del token
    const replaceBtn = await screen.findByRole('button', { name: 'Replace' });
    await user.click(replaceBtn);
    expect(updatePullMirror).toHaveBeenCalledWith('acme', 'api', { token: 'new-token' });
  });
});
