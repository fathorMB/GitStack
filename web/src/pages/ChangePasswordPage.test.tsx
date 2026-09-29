import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { client } from '@gitstack/api-client/src/generated/client.gen';
import { AppRoutes } from '../App';
import { ToastProvider } from '../components';
import { ApiError } from '../lib/http';
import { AuthProvider } from '../routes/auth';

vi.mock('../lib/authApi', () => ({
  fetchOidcProviders: vi.fn().mockResolvedValue([]),
  loginWithPassword: vi.fn(),
  oidcStartUrl: vi.fn(),
  signOut: vi.fn(),
  fetchSession: vi.fn(),
  changeOwnPassword: vi.fn(),
}));

vi.mock('../lib/resourcesApi', () => ({
  ApiError: class ApiError extends Error {},
  fetchResources: vi.fn().mockResolvedValue({ items: [], page: 1, perPage: 20, total: 0 }),
  createTestResource: vi.fn(),
}));

import { changeOwnPassword, fetchSession, loginWithPassword } from '../lib/authApi';

const session = (mustChangePassword: boolean) =>
  ({ user: { username: 'admin' }, authMethod: 'password', mustChangePassword }) as never;

function renderApp(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <ToastProvider>
          <AppRoutes />
        </ToastProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function response403(code: string) {
  return vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify({ error: { code, message: 'no' } }), { status: 403 }));
}

async function fillForm(user: ReturnType<typeof userEvent.setup>, next = 'a-long-new-password', confirm = next) {
  await user.type(await screen.findByLabelText('Current password'), 'old');
  await user.type(screen.getByLabelText('New password'), next);
  await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Change password' }));
}

describe('cambio password obbligatorio', () => {
  beforeEach(() => {
    vi.mocked(loginWithPassword).mockReset();
    vi.mocked(fetchSession).mockReset().mockResolvedValue(session(true));
    vi.mocked(changeOwnPassword).mockReset();
  });

  it('dopo il login con mustChangePassword=true mostra solo la schermata di cambio', async () => {
    vi.mocked(loginWithPassword).mockResolvedValue(session(true));
    const user = userEvent.setup();
    renderApp('/login');
    await user.type(screen.getByLabelText('Username or email'), 'admin');
    await user.type(screen.getByLabelText('Password'), 'initial');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Change your password' })).toBeInTheDocument();
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  });

  it('un 403 password_change_required da una chiamata qualsiasi porta alla schermata', async () => {
    renderApp('/_components');
    expect(await screen.findByRole('heading', { name: 'Components' })).toBeInTheDocument();
    await client.get({ url: '/users', baseUrl: 'http://localhost/api', fetch: response403('password_change_required') });
    expect(await screen.findByRole('heading', { name: 'Change your password' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Components' })).not.toBeInTheDocument();
  });

  it('un 403 con un altro code non fa redirect', async () => {
    renderApp('/_components');
    expect(await screen.findByRole('heading', { name: 'Components' })).toBeInTheDocument();
    await client.get({ url: '/users', baseUrl: 'http://localhost/api', fetch: response403('forbidden') });
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByRole('heading', { name: 'Components' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Change your password' })).not.toBeInTheDocument();
  });

  it('dopo il 204 si torna alla pagina richiesta', async () => {
    vi.mocked(changeOwnPassword).mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderApp('/_components');
    await screen.findByRole('heading', { name: 'Components' });
    await client.get({ url: '/users', baseUrl: 'http://localhost/api', fetch: response403('password_change_required') });
    vi.mocked(fetchSession).mockResolvedValue(session(false));
    await fillForm(user);
    expect(changeOwnPassword).toHaveBeenCalledWith('admin', 'old', 'a-long-new-password');
    expect(await screen.findByRole('heading', { name: 'Components' })).toBeInTheDocument();
  });

  it('un 422 è mostrato sul campo newPassword', async () => {
    vi.mocked(changeOwnPassword).mockRejectedValue(
      new ApiError({ error: { code: 'validation_failed', message: 'Invalid', details: { fields: { newPassword: 'too short' } } } }, 422),
    );
    const user = userEvent.setup();
    renderApp('/change-password');
    await fillForm(user, 'short');
    const field = screen.getByLabelText('New password');
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'));
    expect(screen.getByText('too short')).toBeInTheDocument();
  });

  it('le due password devono coincidere', async () => {
    const user = userEvent.setup();
    renderApp('/change-password');
    await fillForm(user, 'a-long-new-password', 'different');
    expect(await screen.findByText('The two passwords do not match.')).toBeInTheDocument();
    expect(changeOwnPassword).not.toHaveBeenCalled();
  });
});
