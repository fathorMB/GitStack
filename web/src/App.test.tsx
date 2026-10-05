import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { client } from '@gitstack/api-client/src/generated/client.gen';
import { AppRoutes } from './App';
import { ToastProvider } from './components';
import { AuthProvider } from './routes/auth';

vi.mock('./lib/authApi', () => ({
  fetchSession: vi.fn().mockResolvedValue({ user: { username: 'nobody', isAdmin: false } }),
  fetchOidcProviders: vi.fn().mockResolvedValue([]),
  loginWithPassword: vi.fn().mockResolvedValue({}),
  oidcStartUrl: vi.fn(),
  signOut: vi.fn(),
}));

vi.mock('./lib/resourcesApi', () => ({
  ApiError: class ApiError extends Error {},
  fetchResources: vi.fn().mockResolvedValue({ items: [], page: 1, perPage: 20, total: 0 }),
  createTestResource: vi.fn(),
}));

describe('routing protetto', () => {
  it('una risposta 401 dal client generato porta al login', async () => {
    const user = userEvent.setup();
    // Il client generato fissa fetch alla creazione: lo si passa per chiamata.
    const fetch401 = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'no' } }), { status: 401 }));
    render(
      <MemoryRouter initialEntries={['/_components']}>
        <AuthProvider>
          <ToastProvider>
            <AppRoutes />
          </ToastProvider>
        </AuthProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByRole('heading', { name: 'Components' })).toBeInTheDocument();

    // Chiamata reale col client di default: l'interceptor vede il 401.
    await client.get({ url: '/health', baseUrl: 'http://localhost/api', fetch: fetch401 });
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Sign in to GitStack' })).toBeInTheDocument());

    // Dal form di login si torna alla pagina richiesta.
    await user.type(screen.getByLabelText('Username or email'), 'mrossi');
    await user.type(screen.getByLabelText('Password'), 'secret');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Components' })).toBeInTheDocument();
  });
});
