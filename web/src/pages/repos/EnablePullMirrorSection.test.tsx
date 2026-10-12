import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import type { PullMirror } from '../../lib/pullMirrorsApi';
import { EnablePullMirrorSection } from './EnablePullMirrorSection';

vi.mock('../../lib/pullMirrorsApi', () => ({
  enablePullMirror: vi.fn(),
}));

import { enablePullMirror } from '../../lib/pullMirrorsApi';

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
      <EnablePullMirrorSection
        owner="acme"
        repo="api"
        onSuccess={vi.fn()}
      />
    </ToastProvider>,
  );
}

describe('EnablePullMirrorSection', () => {
  beforeEach(() => {
    vi.mocked(enablePullMirror).mockReset();
  });

  it('mostra titolo "Pull mirror" con badge "Off"', async () => {
    renderSection();
    expect(await screen.findByRole('heading', { name: 'Pull mirror' })).toBeInTheDocument();
    expect(screen.getByText('Off')).toBeInTheDocument();
  });

  it('mostra avvertenza MP1: nessun dato cancellato o sovrascritto', async () => {
    renderSection();
    expect(await screen.findByText(/nothing is deleted or overwritten/)).toBeInTheDocument();
  });

  it('il form contiene URL dell\'origine', async () => {
    renderSection();
    expect(screen.getByLabelText('Source URL')).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/https:\/\/github.com/)).toBeInTheDocument();
  });

  it('il form contiene nome utente opzionale', async () => {
    renderSection();
    expect(screen.getByLabelText('Username (optional)')).toBeInTheDocument();
  });

  it('il form contiene token opzionale', async () => {
    renderSection();
    expect(screen.getByLabelText('Access token (optional)')).toBeInTheDocument();
  });

  it('il form contiene selettore intervallo', async () => {
    renderSection();
    expect(screen.getByRole('combobox', { name: /Update interval/i })).toBeInTheDocument();
  });

  it('il pulsante "Enable pull mirror" e\' spento senza URL', async () => {
    renderSection();
    expect(screen.getByRole('button', { name: 'Enable pull mirror' })).toBeDisabled();
  });

  it('il pulsante "Enable pull mirror" si attiva inserendo URL', async () => {
    const user = userEvent.setup();
    renderSection();
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'https://github.com/acme/origin.git');
    expect(screen.getByRole('button', { name: 'Enable pull mirror' })).not.toBeDisabled();
  });

  it('abilita il mirror con URL e intervallo', async () => {
    vi.mocked(enablePullMirror).mockResolvedValue(pullMirror());
    const user = userEvent.setup();
    renderSection();
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'https://github.com/acme/origin.git');
    await user.click(screen.getByRole('button', { name: 'Enable pull mirror' }));
    expect(enablePullMirror).toHaveBeenCalledWith('acme', 'api', {
      url: 'https://github.com/acme/origin.git',
      intervalSeconds: 600,
    });
  });

  it('passa username e token se inseriti', async () => {
    vi.mocked(enablePullMirror).mockResolvedValue(pullMirror());
    const user = userEvent.setup();
    renderSection();
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'https://github.com/acme/origin.git');
    const usernameInput = screen.getByLabelText('Username (optional)');
    await user.type(usernameInput, 'mirror-bot');
    const tokenInput = screen.getByLabelText('Access token (optional)');
    await user.type(tokenInput, 's3cret');
    await user.click(screen.getByRole('button', { name: 'Enable pull mirror' }));
    expect(enablePullMirror).toHaveBeenCalledWith('acme', 'api', {
      url: 'https://github.com/acme/origin.git',
      username: 'mirror-bot',
      token: 's3cret',
      intervalSeconds: 600,
    });
  });

  it('mostra errore di validazione sul campo', async () => {
    vi.mocked(enablePullMirror).mockRejectedValue(
      new ApiError(
        { error: { code: 'validation_failed', message: 'Invalid', details: { fields: { url: 'url must be https' } } } },
        422,
      ),
    );
    const user = userEvent.setup();
    renderSection();
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'http://x');
    await user.click(screen.getByRole('button', { name: 'Enable mirror' }));
    expect(await screen.findByText('url must be https')).toBeInTheDocument();
  });

  it('mostra errore generico in alert', async () => {
    vi.mocked(enablePullMirror).mockRejectedValue(new Error('Server error'));
    const user = userEvent.setup();
    renderSection();
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'https://github.com/acme/origin.git');
    await user.click(screen.getByRole('button', { name: 'Enable mirror' }));
    // describeError per un Error non-API torna il messaggio di connessione.
    expect(await screen.findByText(/Could not reach/)).toBeInTheDocument();
  });

  it('chiama onSuccess al successo e mostra toast', async () => {
    const onSuccessMock = vi.fn();
    vi.mocked(enablePullMirror).mockResolvedValue(pullMirror());
    const user = userEvent.setup();
    render(<ToastProvider><EnablePullMirrorSection owner="acme" repo="api" onSuccess={onSuccessMock} /></ToastProvider>);
    const urlInput = screen.getByLabelText('Source URL');
    await user.type(urlInput, 'https://github.com/acme/origin.git');
    await user.click(screen.getByRole('button', { name: 'Enable pull mirror' }));
    await waitFor(() => expect(onSuccessMock).toHaveBeenCalledWith(expect.objectContaining({ origin: 'https://github.com/acme/origin.git' })));
  });

  it('mostra avvertenza sul contenuto esistente del repo', async () => {
    renderSection();
    expect(await screen.findByText(/This repository already has content/)).toBeInTheDocument();
  });

  it('il pulsante e\' chiamato "Enable pull mirror"', async () => {
    renderSection();
    expect(await screen.findByRole('button', { name: 'Enable pull mirror' })).toBeInTheDocument();
  });
});
