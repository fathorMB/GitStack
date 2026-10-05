import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../lib/http';
import { AuthProvider, RequireAuth } from './auth';

vi.mock('../lib/authApi', () => ({ fetchSession: vi.fn() }));
import { fetchSession } from '../lib/authApi';

const mockedSession = vi.mocked(fetchSession);

function apiError(status: number, code = 'unexpected') {
  return new ApiError({ error: { code, message: `Unexpected response from the server (${status}).` } }, status);
}

function renderGate() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<h1>Sign in page</h1>} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <h1>Private app</h1>
              </RequireAuth>
            }
          />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('RequireAuth: verifica della sessione (GIT-151)', () => {
  beforeEach(() => {
    mockedSession.mockReset();
  });

  it.each([404, 503])('un %i su /auth/session e\' "servizio non raggiungibile", mai l\'app aperta', async (status) => {
    mockedSession.mockImplementation(() => Promise.reject(apiError(status)));
    renderGate();
    expect(await screen.findByRole('heading', { name: 'Service unreachable' })).toBeInTheDocument();
    expect(screen.queryByText('Private app')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('un errore di rete e\' "servizio non raggiungibile"', async () => {
    mockedSession.mockImplementation(() => Promise.reject(new TypeError("Failed to fetch")));
    renderGate();
    expect(await screen.findByRole('heading', { name: 'Service unreachable' })).toBeInTheDocument();
  });

  it('Try again riprova e, a servizio tornato, apre l\'app', async () => {
    const user = userEvent.setup();
    mockedSession.mockRejectedValueOnce(apiError(503)).mockResolvedValue({ user: { username: 'admin' } } as never);
    renderGate();
    await user.click(await screen.findByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('heading', { name: 'Private app' })).toBeInTheDocument();
    expect(mockedSession).toHaveBeenCalledTimes(2);
  });

  it('con sessione valida apre l\'app', async () => {
    mockedSession.mockResolvedValue({ user: { username: 'admin' } } as never);
    renderGate();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Private app' })).toBeInTheDocument());
  });
});
