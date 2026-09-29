import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { SshKeysPage } from './SshKeysPage';

vi.mock('../../lib/accessApi', () => ({
  fetchSshKeys: vi.fn(),
  createSshKey: vi.fn(),
  removeSshKey: vi.fn(),
}));

import { createSshKey, fetchSshKeys, removeSshKey } from '../../lib/accessApi';

const mockedList = vi.mocked(fetchSshKeys);
const mockedCreate = vi.mocked(createSshKey);
const mockedRemove = vi.mocked(removeSshKey);

const key = {
  id: 'k-1',
  title: 'work-laptop',
  keyType: 'ssh-ed25519',
  fingerprint: 'SHA256:q4Z3abc',
  createdAt: '2026-07-01T00:00:00Z',
  lastUsedAt: null,
};

function renderPage() {
  return render(
    <ToastProvider>
      <SshKeysPage />
    </ToastProvider>,
  );
}

describe('SshKeysPage', () => {
  beforeEach(() => {
    mockedList.mockReset().mockResolvedValue([key]);
    mockedCreate.mockReset();
    mockedRemove.mockReset();
  });

  it('elenca le chiavi con il fingerprint', async () => {
    renderPage();
    expect(await screen.findByText('work-laptop')).toBeInTheDocument();
    expect(screen.getByText(/SHA256:q4Z3abc/)).toBeInTheDocument();
  });

  it('stato vuoto', async () => {
    mockedList.mockResolvedValue([]);
    renderPage();
    expect(await screen.findByText('No SSH keys')).toBeInTheDocument();
  });

  it('aggiunge una chiave', async () => {
    mockedCreate.mockResolvedValue({ ...key, id: 'k-2', title: 'build-server', fingerprint: 'SHA256:zzz9' });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('work-laptop');
    await user.click(screen.getByRole('button', { name: /add ssh key/i }));
    await user.type(screen.getByLabelText('Title'), 'build-server');
    await user.type(screen.getByLabelText('Public key'), 'ssh-ed25519 AAAAC3 me@host');
    await user.click(within(screen.getByRole('form', { name: 'New SSH key' })).getByRole('button', { name: 'Add SSH key' }));
    await waitFor(() =>
      expect(mockedCreate).toHaveBeenCalledWith({ title: 'build-server', publicKey: 'ssh-ed25519 AAAAC3 me@host' }),
    );
    expect(await screen.findByText(/SHA256:zzz9/)).toBeInTheDocument();
  });

  it('mostra l\'errore dell\'API se la chiave e\' rifiutata (422)', async () => {
    mockedCreate.mockRejectedValue(
      new ApiError({ error: { code: 'validation_failed', message: 'Invalid key', details: { fields: { publicKey: 'not a valid key' } } } }, 422),
    );
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('work-laptop');
    await user.click(screen.getByRole('button', { name: /add ssh key/i }));
    await user.type(screen.getByLabelText('Title'), 't');
    await user.type(screen.getByLabelText('Public key'), 'nope');
    await user.click(within(screen.getByRole('form', { name: 'New SSH key' })).getByRole('button', { name: 'Add SSH key' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('publicKey: not a valid key');
  });

  it('cancella una chiave dopo conferma', async () => {
    mockedRemove.mockResolvedValue();
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Delete work-laptop' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockedRemove).toHaveBeenCalledWith('k-1'));
    await waitFor(() => expect(screen.queryByText('work-laptop')).not.toBeInTheDocument());
  });
});
