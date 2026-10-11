// Controllo di fumo di notifiche, preferenze e webhook sullo stack vero (GIT-141, M-06).
//
// Non gira da solo: lo lancia il test Go `TestM06E2E` (services/core,
// internal/stackitest, sottotest `ui_smoke`) dopo aver preparato il repo, le
// notifiche e i webhook, e gli passa gateway e token con le variabili
// VITE_SMOKE_*. Senza quelle variabili i test sono saltati, così `pnpm test`
// resta veloce e senza stack. Stesso schema di issues.smoke.test.tsx.
//
// Pagine dei mockup: 05 (centro notifiche), 11 (webhook del repo) e 21
// (impostazioni delle notifiche), con le rotte vere di App.tsx.
import { configure, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../App';
import { ToastProvider } from '../components';
import { AuthProvider } from '../routes/auth';

const gateway = import.meta.env.VITE_SMOKE_GATEWAY as string | undefined;
const token = import.meta.env.VITE_SMOKE_TOKEN as string | undefined;

const hookRepo = import.meta.env.VITE_SMOKE_HOOK_REPO as string | undefined;
const hookUrl = import.meta.env.VITE_SMOKE_HOOK_URL as string | undefined;

const enabled = Boolean(gateway && token && hookRepo && hookUrl);

// Base dell'API: il gateway vero invece del `/api` relativo del container web.
vi.mock('../lib/http', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/http')>()),
  API_BASE_URL: `${import.meta.env.VITE_SMOKE_GATEWAY ?? 'http://unused.invalid'}/v1`,
}));

configure({ asyncUtilTimeout: 15_000 });

const pages: { screen: string; path: string; expect: (v: ReturnType<typeof within>) => Promise<unknown> }[] = [
  {
    screen: '05 centro notifiche (non lette, filtri per motivo)',
    path: '/notifications',
    expect: async (v) => {
      expect(await v.findByRole('heading', { name: 'Notifications' })).toBeInTheDocument();
      expect((await v.findAllByTestId('notification')).length).toBeGreaterThan(0);
      expect(await v.findByRole('group', { name: 'Reason' })).toBeInTheDocument();
    },
  },
  {
    screen: '11 webhook del repo (elenco con indirizzo, eventi e ultima consegna)',
    path: `/${hookRepo}/webhooks`,
    expect: async (v) => {
      expect(await v.findByRole('heading', { name: 'Webhooks' })).toBeInTheDocument();
      expect(await v.findByText(hookUrl as string)).toBeInTheDocument();
    },
  },
  {
    screen: '21 impostazioni delle notifiche (email per tipo, Watch, conservazione)',
    path: '/settings/notifications',
    expect: async (v) => {
      expect(await v.findByRole('region', { name: 'Notification preferences' })).toBeInTheDocument();
      expect(await v.findByRole('region', { name: 'Watching' })).toBeInTheDocument();
      expect(await v.findByRole('region', { name: 'Retention' })).toBeInTheDocument();
    },
  },
];

describe.skipIf(!enabled)('smoke UI di notifiche e webhook (stack vero)', { timeout: 60_000 }, () => {
  const apiCalls: { url: string; status: number }[] = [];

  beforeAll(() => {
    const realFetch = globalThis.fetch.bind(globalThis);
    vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const headers = new Headers(req.headers);
      headers.set('Authorization', `Bearer ${token}`);
      const res = await realFetch(new Request(req, { headers }));
      const u = new URL(req.url);
      apiCalls.push({ url: u.pathname + u.search, status: res.status });
      return res;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it.each(pages)('$screen si carica senza errori', async (page) => {
    const errors: unknown[][] = [];
    vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      errors.push(args);
    });
    apiCalls.length = 0;

    const { container } = render(
      <MemoryRouter initialEntries={[page.path]}>
        <AuthProvider>
          <ToastProvider>
            <AppRoutes />
          </ToastProvider>
        </AuthProvider>
      </MemoryRouter>,
    );
    try {
      await page.expect(within(container));
    } catch (err) {
      const text = (container.querySelector('main') ?? container).textContent?.replace(/\s+/g, ' ').slice(0, 600);
      throw new Error(`${String(err).split('\n')[0]} | pagina: ${text} | api: ${JSON.stringify(apiCalls)} | console.error: ${JSON.stringify(errors.map((e) => String(e[0]).slice(0, 200)))}`);
    }

    await waitFor(() => expect(apiCalls.length).toBeGreaterThan(0));
    const bad = apiCalls.filter((c) => c.status >= 400);
    expect(bad, `chiamate fallite: ${JSON.stringify(bad)}`).toEqual([]);
    expect(screen.queryAllByRole('alert')).toEqual([]);
    expect(errors, `console.error: ${JSON.stringify(errors.map((e) => String(e[0]).slice(0, 200)))}`).toEqual([]);
  });
});
