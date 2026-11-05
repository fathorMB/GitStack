import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../lib/http';
import { UserAppsPage } from './UserAppsPage';

vi.mock('../lib/appsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/appsApi')>()),
  fetchUserApps: vi.fn(),
}));

import { fetchUserApps } from '../lib/appsApi';
import type { App, UserAppList } from '../lib/appsApi';

const mocked = vi.mocked(fetchUserApps);

function app(over: Partial<App>): App {
  return {
    id: 'a-1', name: 'web', owner: 'alice', repo: 'handbook', status: 'running', access: 'login',
    address: 'https://web-handbook-alice.apps.example.com', image: { source: 'registry', reference: 'x', verified: true },
    activeVersion: 3, copies: 1, readyCopies: 1,
    resources: { guaranteed: { cpuMillis: 100, memoryBytes: 1 }, limit: { cpuMillis: 100, memoryBytes: 1 } },
    createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...over,
  } as App;
}

function list(over: Partial<UserAppList>): UserAppList {
  return { items: [], page: 1, perPage: 20, total: 0, hostingEnabled: true, ...over };
}

function renderPage() {
  return render(
    <MemoryRouter>
      <UserAppsPage />
    </MemoryRouter>,
  );
}

describe('UserAppsPage', () => {
  beforeEach(() => {
    mocked.mockReset();
  });

  it('mostra repo, nome, stato, indirizzo e versione, con link alla pagina apps del repo', async () => {
    mocked.mockResolvedValue(list({ items: [app({})], total: 1 }));
    renderPage();
    const link = await screen.findByRole('link', { name: 'alice/handbook' });
    expect(link).toHaveAttribute('href', '/alice/handbook/apps');
    expect(screen.getByText('web')).toBeInTheDocument();
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('https://web-handbook-alice.apps.example.com')).toBeInTheDocument();
    expect(screen.getByText(/v3/)).toBeInTheDocument();
  });

  it('con hosting spento dice che e\' spento, senza errore', async () => {
    mocked.mockResolvedValue(list({ hostingEnabled: false }));
    renderPage();
    expect(await screen.findByText('App hosting is turned off')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('senza app mostra lo stato vuoto', async () => {
    mocked.mockResolvedValue(list({}));
    renderPage();
    expect(await screen.findByText('No apps yet')).toBeInTheDocument();
  });

  it('pagina: Next chiede la pagina 2', async () => {
    mocked.mockResolvedValueOnce(list({ items: [app({})], total: 25 }));
    mocked.mockResolvedValueOnce(list({ items: [app({ id: 'a-2', name: 'api' })], total: 25, page: 2 }));
    renderPage();
    await userEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(await screen.findByText('api')).toBeInTheDocument();
    expect(mocked).toHaveBeenLastCalledWith(2, 20);
  }, 15000);

  it('un errore di caricamento e\' mostrato come errore', async () => {
    mocked.mockRejectedValue(new ApiError({ error: { code: 'internal_error', message: 'boom' } }, 500));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('boom');
  });
});
