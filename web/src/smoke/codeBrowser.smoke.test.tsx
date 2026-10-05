// Controllo di fumo del browser del codice sullo stack vero (GIT-89, M-04/J).
//
// Non gira da solo: lo lancia il test Go `TestBrowserCodice` (services/core,
// internal/stackitest, sottotest `ui_smoke`) dopo aver preparato il repo di
// prova, e gli passa l'indirizzo del gateway e un token con le variabili
// VITE_SMOKE_*. Senza quelle variabili i test sono saltati, così
// `pnpm test` resta veloce e senza stack.
//
// Strumento: Vitest + Testing Library + jsdom, quello del resto di web/ (nessun
// browser vero da installare in CI). La SPA chiama `/api/...`: qui `fetch` le
// riscrive su `<gateway>/v1/...` aggiungendo il token, come farebbe il container
// web con la sessione. Le pagine sono le vere di App.tsx, con le rotte vere.
import { configure, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../App';
import { ToastProvider } from '../components';
import { AuthProvider } from '../routes/auth';

const gateway = import.meta.env.VITE_SMOKE_GATEWAY as string | undefined;
const token = import.meta.env.VITE_SMOKE_TOKEN as string | undefined;
const repo = (import.meta.env.VITE_SMOKE_REPO as string | undefined) ?? 'alice/quarzo-codice';
const bigSha = import.meta.env.VITE_SMOKE_SHA as string | undefined;
const needle = (import.meta.env.VITE_SMOKE_NEEDLE as string | undefined) ?? 'LORENZO-UNICO-5c2e';

const enabled = Boolean(gateway && token && bigSha);

// Base dell'API: il gateway vero invece del `/api` relativo del container web.
vi.mock('../lib/http', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/http')>()),
  API_BASE_URL: `${import.meta.env.VITE_SMOKE_GATEWAY ?? 'http://unused.invalid'}/v1`,
}));

// Rete vera e Postgres vero: i tempi di default (1 s) di Testing Library sono troppo stretti.
configure({ asyncUtilTimeout: 15_000 });

// Le pagine del browser del codice dei mockup 07, 08, 09 e 10, più la 22 (Blame),
// la 23 (Tags) e la pagina dei risultati di Search code (GIT-94).
const pages: { screen: string; path: string; expect: (v: ReturnType<typeof within>) => Promise<unknown> }[] = [
  {
    screen: '07 repo (albero e README)',
    path: `/${repo}`,
    expect: async (v) => {
      expect(await v.findByText('package-lock.json')).toBeInTheDocument();
      expect(await v.findByRole('heading', { name: /Progetto Quarzo/ })).toBeInTheDocument();
    },
  },
  {
    screen: '08 file (evidenziato, con blame)',
    path: `/${repo}/blob/main/main.go`,
    expect: async (v) => {
      expect((await v.findAllByText(/quarzo dal bot/)).length).toBeGreaterThan(0);
    },
  },
  {
    screen: '22 blame (blocchi per commit)',
    path: `/${repo}/blame/main/main.go`,
    expect: async (v) => {
      expect((await v.findAllByText(/modifica del bot/)).length).toBeGreaterThan(0);
      expect((await v.findAllByText(/primo commit/)).length).toBeGreaterThan(0);
      expect((await v.findAllByText('agent')).length).toBeGreaterThan(0);
    },
  },
  {
    screen: '09 storico dei commit',
    path: `/${repo}/commits`,
    expect: async (v) => {
      expect(await v.findByText('modifica del bot')).toBeInTheDocument();
      expect(await v.findByText('Merge feature/uno')).toBeInTheDocument();
    },
  },
  {
    screen: '10 dettaglio del commit (file chiusi di default)',
    path: `/${repo}/commit/${bigSha}`,
    expect: async (v) => {
      expect((await v.findAllByText(/big\.txt/)).length).toBeGreaterThan(0);
      expect((await v.findAllByText(/package-lock\.json/)).length).toBeGreaterThan(0);
    },
  },
  {
    screen: '23 tag (annotato col messaggio, leggero senza)',
    path: `/${repo}/tags`,
    expect: async (v) => {
      expect(await v.findByText('v1.0')).toBeInTheDocument();
      expect(await v.findByText(/release 1\.0 del progetto quarzo/)).toBeInTheDocument();
      expect(await v.findByText('v0.1')).toBeInTheDocument();
      expect(await v.findByText(/Lightweight tag/)).toBeInTheDocument();
    },
  },
  {
    screen: 'risultati di Search code',
    path: `/${repo}/search?q=${encodeURIComponent(needle)}`,
    expect: async (v) => {
      expect(await v.findByRole('region', { name: 'main.go' })).toBeInTheDocument();
      expect(await v.findByRole('region', { name: 'docs/guida.md' })).toBeInTheDocument();
    },
  },
];

describe.skipIf(!enabled)('smoke UI del browser del codice (stack vero)', { timeout: 60_000 }, () => {
  const apiCalls: { url: string; status: number }[] = [];

  beforeAll(() => {
    // La SPA chiama `/api/...` (base relativa, vedi lib/http.ts, mockato sotto con
    // l'indirizzo vero del gateway); qui si aggiunge solo il token e si contano
    // le risposte.
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
      // il DOM di Testing Library è enorme: qui solo il testo della pagina e le chiamate fatte
      const text = (container.querySelector('main') ?? container).textContent?.replace(/\s+/g, ' ').slice(0, 600);
      throw new Error(`${String(err).split('\n')[0]} | pagina: ${text} | api: ${JSON.stringify(apiCalls)} | console.error: ${JSON.stringify(errors.map((e) => String(e[0]).slice(0, 200)))}`);
    }

    // nessuna chiamata dell'API è andata male, né un 404 né un 5xx
    await waitFor(() => expect(apiCalls.length).toBeGreaterThan(0));
    // TODO(GIT-106): il contatore delle issue aperte nella scheda del repo (GIT-109) chiama
    // searchIssues, che in core risponde 501 finché GIT-106 non è su main; togliere l'eccezione allora.
    const bad = apiCalls.filter((c) => c.status >= 400 && !(c.status === 501 && //repos/[^/]+/[^/]+/issues?/.test(c.url)));
    // il README di una sottocartella può non esserci (404 atteso), ma qui è la radice
    expect(bad, `chiamate fallite: ${JSON.stringify(bad)}`).toEqual([]);
    // nessun avviso di errore nella pagina (ruolo alert) né console.error di React
    expect(screen.queryAllByRole('alert')).toEqual([]);
    expect(errors, `console.error: ${JSON.stringify(errors.map((e) => String(e[0]).slice(0, 200)))}`).toEqual([]);
  });
});
