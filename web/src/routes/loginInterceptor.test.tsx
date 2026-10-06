import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../App';
import { ToastProvider } from '../components';
import { AuthProvider } from './auth';

// GIT-153: login con credenziali sbagliate attraverso il vero client generato
// e il vero interceptor 401 (niente mock di loginWithPassword: si finge solo
// fetch). Il 401 di POST /auth/login deve restare un errore del form, non
// svuotare i campi né rimandare a /login con un rimontaggio della pagina.

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function Where() {
  const loc = useLocation();
  return <output data-testid="where">{loc.pathname}</output>;
}

describe('login con credenziali sbagliate (interceptor 401 reale)', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('resta su /login, mostra l’errore e conserva il valore dello username', async () => {
    const calls: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const req = input instanceof Request ? input : new Request(input, init);
      const url = new URL(req.url, 'http://localhost');
      calls.push(`${req.method} ${url.pathname}`);
      if (url.pathname.endsWith('/auth/login')) {
        return Promise.resolve(
          json(401, { error: { code: 'invalid_credentials', message: 'Invalid credentials.' } }),
        );
      }
      if (url.pathname.endsWith('/auth/session')) {
        return Promise.resolve(json(401, { error: { code: 'unauthenticated', message: 'Authentication required.' } }));
      }
      if (url.pathname.endsWith('/auth/oidc/providers')) {
        return Promise.resolve(json(200, { items: [] }));
      }
      return Promise.resolve(json(404, { error: { code: 'not_found', message: 'nope' } }));
    });
    vi.stubGlobal('fetch', fetchMock);
    // In jsdom/Node new Request('/api/v1/...') con URL relativo lancia (nel
    // browser si risolve sull'origine): il client lo inghiottirebbe come
    // errore muto. Si risolve sull'origine di prova, senza toccare il client.
    const NativeRequest = Request;
    vi.stubGlobal(
      'Request',
      class extends NativeRequest {
        constructor(input: RequestInfo | URL, init?: RequestInit) {
          super(typeof input === 'string' && input.startsWith('/') ? `http://localhost${input}` : input, init);
        }
      },
    );

    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/login']}>
        <AuthProvider>
          <ToastProvider>
            <Where />
            <AppRoutes />
          </ToastProvider>
        </AuthProvider>
      </MemoryRouter>,
    );

    const username = await screen.findByLabelText('Username or email');
    await user.type(username, 'mrossi');
    await user.type(screen.getByLabelText('Password'), 'sbagliata-per-niente');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    // (b) messaggio d'errore del form
    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong username or password.');
    expect(calls.some((c) => c.startsWith('POST') && c.endsWith('/auth/login'))).toBe(true);

    // (a) nessuna navigazione e nessun rimontaggio: stessa rotta, stesso nodo
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/login$/));
    expect(screen.getByLabelText('Username or email')).toBe(username);

    // (c) il campo username conserva il valore scritto
    expect(username).toHaveValue('mrossi');
  });
});
