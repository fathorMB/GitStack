import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { TokensPage } from './TokensPage';

// Come ResourcesPage.test: si isola il wrapper del client (risposte finte,
// /user/tokens risponde 501 nel binario finche' non c'e' GIT-51).
vi.mock('../../lib/accessApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/accessApi')>()),
  fetchTokens: vi.fn(),
  createPersonalToken: vi.fn(),
  revokePersonalToken: vi.fn(),
}));

import { createPersonalToken, fetchTokens, revokePersonalToken } from '../../lib/accessApi';

const mockedList = vi.mocked(fetchTokens);
const mockedCreate = vi.mocked(createPersonalToken);
const mockedRevoke = vi.mocked(revokePersonalToken);

const existing = {
  id: 't-1',
  name: 'laptop-cli',
  scopes: ['read:user' as const],
  hint: 'ab12',
  createdAt: '2026-09-01T00:00:00Z',
  expiresAt: '2026-12-01T00:00:00Z',
  lastUsedAt: null,
};

function renderPage(entry = '/settings/tokens', startCreating = false) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <ToastProvider>
        <TokensPage startCreating={startCreating} />
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe('TokensPage', () => {
  beforeEach(() => {
    mockedList.mockReset().mockResolvedValue([existing]);
    mockedCreate.mockReset();
    mockedRevoke.mockReset();
  });

  it('elenca i token con scope e senza valore', async () => {
    renderPage();
    expect(await screen.findByText('laptop-cli')).toBeInTheDocument();
    expect(screen.getByText('read:user')).toBeInTheDocument();
    expect(screen.getByText(/…ab12/)).toBeInTheDocument();
  });

  it('crea un token con scope e scadenza; il valore si vede una volta sola', async () => {
    mockedCreate.mockResolvedValue({ ...existing, id: 't-2', name: 'claude-code', scopes: ['read:org'], token: 'gst_SECRET123' });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('laptop-cli');

    await user.click(screen.getByRole('button', { name: /generate token/i }));
    await user.type(screen.getByLabelText('Name'), 'claude-code');
    await user.click(screen.getByRole('checkbox', { name: 'read:org' }));
    await user.click(within(screen.getByRole('form', { name: 'New token' })).getByRole('button', { name: 'Generate token' }));

    await waitFor(() => expect(mockedCreate).toHaveBeenCalledTimes(1));
    const input = mockedCreate.mock.calls[0]?.[0];
    expect(input?.name).toBe('claude-code');
    expect(input?.scopes).toEqual(['read:org']);
    expect(input?.expiresAt).toBeTruthy();

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('New token value')).toHaveValue('gst_SECRET123');
    expect(within(dialog).getByRole('button', { name: /copy/i })).toBeInTheDocument();
    expect(within(dialog).getByText(/you won.t be able to see it again/i)).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    // Il valore non e' piu' da nessuna parte; il token resta in elenco senza valore.
    expect(screen.queryByDisplayValue('gst_SECRET123')).not.toBeInTheDocument();
    expect(screen.queryByText('gst_SECRET123')).not.toBeInTheDocument();
    expect(screen.getByText('claude-code')).toBeInTheDocument();
  });

  it('chiede almeno uno scope', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('laptop-cli');
    await user.click(screen.getByRole('button', { name: /generate token/i }));
    await user.type(screen.getByLabelText('Name'), 'x');
    await user.click(within(screen.getByRole('form', { name: 'New token' })).getByRole('button', { name: 'Generate token' }));
    expect(await screen.findByText('Select at least one scope.')).toBeInTheDocument();
    expect(mockedCreate).not.toHaveBeenCalled();
  });

  it('revoca un token dopo conferma', async () => {
    mockedRevoke.mockResolvedValue();
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Revoke laptop-cli' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(mockedRevoke).toHaveBeenCalledWith('t-1'));
    await waitFor(() => expect(screen.queryByText('laptop-cli')).not.toBeInTheDocument());
  });

  it('403: messaggio leggibile con il motivo', async () => {
    mockedList.mockRejectedValue(new ApiError({ error: { code: 'insufficient_scope', message: 'Missing scope read:user.' } }, 403));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent("You don't have permission to do this. Missing scope read:user.");
  });
  it('/settings/tokens/new: modulo aperto e precompilato da nome, scope e scadenza', async () => {
    mockedCreate.mockResolvedValue({ ...existing, id: 't-3', name: 'gs', scopes: ['read:user', 'write:resource'], token: 'gst_X' });
    const user = userEvent.setup();
    renderPage('/settings/tokens/new?name=gs&scopes=read:user,write:resource&expires=30', true);
    const form = await screen.findByRole('form', { name: 'New token' });
    expect(within(form).getByLabelText('Name')).toHaveValue('gs');
    expect(screen.getByRole('checkbox', { name: 'read:user' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'write:resource' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'read:org' })).not.toBeChecked();
    expect(screen.queryByRole('status', { name: '' })).toBeNull();
    // Niente si crea da solo: serve la conferma dell'utente.
    expect(mockedCreate).not.toHaveBeenCalled();
    await user.click(within(form).getByRole('button', { name: 'Generate token' }));
    await waitFor(() => expect(mockedCreate).toHaveBeenCalledTimes(1));
    expect(mockedCreate.mock.calls[0]?.[0]).toMatchObject({ name: 'gs', scopes: ['read:user', 'write:resource'] });
  }, 15000);

  it('scope sconosciuti e scadenza non ammessa: ignorati con avviso che li nomina', async () => {
    renderPage('/settings/tokens/new?name=gs&scopes=repo,read:user,issues&expires=7', true);
    const form = await screen.findByRole('form', { name: 'New token' });
    expect(within(form).getByText(/Ignored unknown scopes: repo, issues/)).toBeInTheDocument();
    expect(within(form).getByText(/Ignored expiration "7"/)).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'read:user' })).toBeChecked();
    expect(screen.getAllByRole('checkbox').filter((c) => (c as HTMLInputElement).getAttribute('aria-checked') === 'true')).toHaveLength(1);
  }, 15000);

  it('senza startCreating la query string non apre il modulo', async () => {
    renderPage('/settings/tokens?scopes=read:user');
    await screen.findByText('laptop-cli');
    expect(screen.queryByRole('form', { name: 'New token' })).not.toBeInTheDocument();
  });
});
