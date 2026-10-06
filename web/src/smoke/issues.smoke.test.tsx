// Controllo di fumo delle issues sullo stack vero (GIT-113, M-05).
//
// Non gira da solo: lo lancia il test Go `TestIssuesE2E` (services/core,
// internal/stackitest, sottotest `ui_smoke`) dopo aver preparato il repo e le
// issues, e gli passa gateway, token, repo e numero della issue con le variabili
// VITE_SMOKE_*. Senza quelle variabili i test sono saltati, così `pnpm test` resta
// veloce e senza stack. Stesso schema di codeBrowser.smoke.test.tsx.
//
// Pagine dei mockup: 12 (lista delle issues), 13 (dettaglio) e 20 (nuova issue),
// con le rotte vere di App.tsx.
import { configure, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../App';
import { ToastProvider } from '../components';
import { AuthProvider } from '../routes/auth';

const gateway = import.meta.env.VITE_SMOKE_GATEWAY as string | undefined;
const token = import.meta.env.VITE_SMOKE_TOKEN as string | undefined;
const repo = import.meta.env.VITE_SMOKE_ISSUES_REPO as string | undefined;
const issue = import.meta.env.VITE_SMOKE_ISSUE as string | undefined;

const enabled = Boolean(gateway && token && repo && issue);

// Base dell'API: il gateway vero invece del `/api` relativo del container web.
vi.mock('../lib/http', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/http')>()),
  API_BASE_URL: `${import.meta.env.VITE_SMOKE_GATEWAY ?? 'http://unused.invalid'}/v1`,
}));

configure({ asyncUtilTimeout: 15_000 });

const pages: { screen: string; path: string; expect: (v: ReturnType<typeof within>) => Promise<unknown> }[] = [
  {
    screen: '12 lista delle issues',
    path: `/${repo}/issues`,
    expect: async (v) => {
      expect(await v.findByTestId(`issue-${issue}`)).toBeInTheDocument();
      expect(await v.findByText(/Crash all'avvio/)).toBeInTheDocument();
      expect(await v.findByLabelText('Search issues')).toBeInTheDocument();
    },
  },
  {
    screen: '13 dettaglio della issue (commenti, etichette, assegnatario)',
    path: `/${repo}/issues/${issue}`,
    expect: async (v) => {
      expect((await v.findAllByText(/Crash all'avvio/)).length).toBeGreaterThan(0);
      expect(await v.findByText('succede sempre')).toBeInTheDocument();
      expect((await v.findAllByText('area-rete')).length).toBeGreaterThan(0);
      expect((await v.findAllByText('botty')).length).toBeGreaterThan(0);
    },
  },
  {
    screen: '20 nuova issue (con i modelli del repo)',
    path: `/${repo}/issues/new`,
    expect: async (v) => {
      expect(await v.findByRole('radiogroup', { name: 'Issue template' })).toBeInTheDocument();
      expect(await v.findByLabelText('Title')).toBeInTheDocument();
    },
  },
];

describe.skipIf(!enabled)('smoke UI delle issues (stack vero)', { timeout: 60_000 }, () => {
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
