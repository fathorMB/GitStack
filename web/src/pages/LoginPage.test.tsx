import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LoginPage } from './LoginPage';
import { ApiError } from '../lib/http';
import { AuthProvider } from '../routes/auth';

vi.mock('../lib/authApi', () => ({
  SECOND_FACTOR_REQUIRED: 'second_factor_required',
  fetchOidcProviders: vi.fn(),
  loginWithPassword: vi.fn(),
  oidcStartUrl: (slug: string) => `/api/v1/auth/oidc/${slug}/start`,
}));

import { fetchOidcProviders, loginWithPassword } from '../lib/authApi';

const mockedProviders = vi.mocked(fetchOidcProviders);
const mockedLogin = vi.mocked(loginWithPassword);

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<h1>Home page</h1>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Username or email'), 'mrossi');
  await user.type(screen.getByLabelText('Password'), 'pw');
  await user.click(screen.getByRole('button', { name: 'Sign in' }));
}

describe('LoginPage', () => {
  beforeEach(() => {
    mockedProviders.mockReset().mockResolvedValue([]);
    mockedLogin.mockReset();
  });

  it('con lista provider vuota (o 501/errore) mostra solo il form password, senza errori', async () => {
    renderLogin();
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.queryByText(/continue with/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('mostra i pulsanti dei provider OIDC configurati', async () => {
    mockedProviders.mockResolvedValue([{ slug: 'entra', displayName: 'Microsoft Entra ID' }]);
    renderLogin();
    const link = await screen.findByRole('link', { name: /continue with microsoft entra id/i });
    expect(link).toHaveAttribute('href', expect.stringContaining('/auth/oidc/entra/start'));
  });

  it('accede con la password e va alla pagina richiesta', async () => {
    mockedLogin.mockResolvedValue({} as never);
    const user = userEvent.setup();
    renderLogin();
    await fill(user);
    expect(mockedLogin).toHaveBeenCalledWith('mrossi', 'pw');
    expect(await screen.findByRole('heading', { name: 'Home page' })).toBeInTheDocument();
  });

  it('401 invalid_credentials: messaggio leggibile e resta sul login', async () => {
    mockedLogin.mockRejectedValue(new ApiError({ error: { code: 'invalid_credentials', message: 'x' } }, 401));
    const user = userEvent.setup();
    renderLogin();
    await fill(user);
    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong username or password.');
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
  });

  it('429 too_many_attempts: mostra dopo quanto riprovare (Retry-After)', async () => {
    mockedLogin.mockRejectedValue(new ApiError({ error: { code: 'too_many_attempts', message: 'x' } }, 429, 30));
    const user = userEvent.setup();
    renderLogin();
    await fill(user);
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again in 30 seconds');
  });

  // F11: con il TOTP attivo il primo passo risponde 401 second_factor_required;
  // la UI chiede il codice e ripete il login con username, password e codice.
  describe('secondo fattore (TOTP)', () => {
    const second = () =>
      new ApiError({ error: { code: 'second_factor_required', message: 'Serve il codice del secondo fattore.' } }, 401);

    it('chiede il codice e completa il login a due passi', async () => {
      mockedLogin.mockRejectedValueOnce(second()).mockResolvedValueOnce({} as never);
      const user = userEvent.setup();
      renderLogin();
      await fill(user);

      expect(await screen.findByLabelText('Authentication code')).toBeInTheDocument();
      expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
      expect(mockedLogin).toHaveBeenCalledTimes(1);

      await user.type(screen.getByLabelText('Authentication code'), '123 456');
      await user.click(screen.getByRole('button', { name: 'Verify' }));

      expect(mockedLogin).toHaveBeenLastCalledWith('mrossi', 'pw', { totpCode: '123456' });
      expect(await screen.findByRole('heading', { name: 'Home page' })).toBeInTheDocument();
    });

    it('con un codice sbagliato resta sul secondo passo con un messaggio chiaro', async () => {
      mockedLogin
        .mockRejectedValueOnce(second())
        .mockRejectedValueOnce(new ApiError({ error: { code: 'invalid_credentials', message: 'x' } }, 401));
      const user = userEvent.setup();
      renderLogin();
      await fill(user);
      await user.type(await screen.findByLabelText('Authentication code'), '000000');
      await user.click(screen.getByRole('button', { name: 'Verify' }));

      expect(await screen.findByRole('alert')).toHaveTextContent(/code is not valid/i);
      expect(screen.getByLabelText('Authentication code')).toBeInTheDocument();
    });

    it('accetta un codice di recupero', async () => {
      mockedLogin.mockRejectedValueOnce(second()).mockResolvedValueOnce({} as never);
      const user = userEvent.setup();
      renderLogin();
      await fill(user);
      await user.click(await screen.findByRole('button', { name: 'Use a recovery code' }));
      await user.type(screen.getByLabelText('Recovery code'), 'ABCDE-FGHJK');
      await user.click(screen.getByRole('button', { name: 'Verify' }));

      expect(mockedLogin).toHaveBeenLastCalledWith('mrossi', 'pw', { recoveryCode: 'ABCDE-FGHJK' });
      expect(await screen.findByRole('heading', { name: 'Home page' })).toBeInTheDocument();
    });

    it('Back torna al form della password', async () => {
      mockedLogin.mockRejectedValueOnce(second());
      const user = userEvent.setup();
      renderLogin();
      await fill(user);
      await user.click(await screen.findByRole('button', { name: 'Back' }));
      expect(screen.getByLabelText('Password')).toHaveValue('');
      expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    });
  });
});
