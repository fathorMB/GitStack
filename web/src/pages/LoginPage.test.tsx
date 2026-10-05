import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LoginPage } from './LoginPage';
import { ApiError } from '../lib/http';
import { AuthProvider } from '../routes/auth';

vi.mock('../lib/authApi', () => ({
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
});
