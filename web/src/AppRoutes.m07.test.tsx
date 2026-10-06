import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
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
vi.mock('./lib/notificationsApi', async (orig) => ({
  ...(await orig<typeof import('./lib/notificationsApi')>()),
  fetchUnreadCount: vi.fn().mockResolvedValue(0),
}));
vi.mock('./lib/accessApi', async (orig) => ({
  ...(await orig<typeof import('./lib/accessApi')>()),
  fetchTokens: vi.fn().mockResolvedValue([]),
}));

function Where() {
  const l = useLocation();
  return <output data-testid="where">{l.pathname + l.search}</output>;
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <AuthProvider>
        <ToastProvider>
          <AppRoutes />
          <Where />
        </ToastProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('rotte M-07/K', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('/downloads e\' pubblica: nessuna verifica di sessione, nessun login', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('no', { status: 404 })));
    renderAt('/downloads');
    expect(await screen.findByRole('heading', { name: 'CLI & skills' })).toBeInTheDocument();
    expect(screen.getByTestId('where')).toHaveTextContent('/downloads');
  }, 15000);

  it('/settings/tokens/new apre il modulo precompilato', async () => {
    renderAt('/settings/tokens/new?name=gs&scopes=read:user,write:resource');
    const form = await screen.findByRole('form', { name: 'New token' }, { timeout: 5000 });
    expect(form).toBeInTheDocument();
    expect(screen.getByLabelText('Name')).toHaveValue('gs');
    expect(screen.getByRole('checkbox', { name: 'write:resource' })).toBeChecked();
  }, 15000);

  it('dopo un 401 e il login si torna a /settings/tokens/new con la query intatta', async () => {
    const user = userEvent.setup();
    renderAt('/settings/tokens/new?name=gs&scopes=read:user,write:resource');
    await screen.findByRole('form', { name: 'New token' }, { timeout: 5000 });
    const fetch401 = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'no' } }), { status: 401 }));
    await client.get({ url: '/health', baseUrl: 'http://localhost/api', fetch: fetch401 });
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Sign in to GitStack' })).toBeInTheDocument());
    await user.type(screen.getByLabelText('Username or email'), 'mrossi');
    await user.type(screen.getByLabelText('Password'), 'secret');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('form', { name: 'New token' }, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByTestId('where')).toHaveTextContent('/settings/tokens/new?name=gs&scopes=read:user,write:resource');
    expect(screen.getByLabelText('Name')).toHaveValue('gs');
  }, 20000);
});
