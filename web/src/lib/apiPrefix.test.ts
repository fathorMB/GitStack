import { afterEach, describe, expect, it, vi } from 'vitest';
import { client } from '@gitstack/api-client/src/generated/client.gen';
import { fetchSession } from './authApi';
import { API_BASE_URL } from './http';

// GIT-151: l'URL che la UI manda davvero al fetch deve essere /api/v1/...:
// l'Ingress (e nginx, e Vite) tolgono solo /api e il gateway riceve /v1/...
describe("prefisso pubblico dell'API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it('la chiamata di sessione esce verso /api/v1/auth/session', async () => {
    const seen: string[] = [];
    // jsdom/undici non accettano `new Request('/relativo')`: nel browser la base e' l'origine.
    const RealRequest = Request;
    vi.stubGlobal(
      'Request',
      class extends RealRequest {
        constructor(input: RequestInfo | URL, init?: RequestInit) {
          super(typeof input === 'string' && input.startsWith('/') ? `http://localhost${input}` : input, init);
        }
      },
    );
    // Il client generato fissa fetch alla creazione: lo si imposta sul client di default.
    client.setConfig({
      fetch: (async (req: Request) => {
        seen.push(req.url);
        return new Response(JSON.stringify({ user: { username: 'admin' } }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }) as unknown as typeof fetch,
    });
    await fetchSession().catch(() => undefined);
    expect(seen).toHaveLength(1);
    expect(new URL(seen[0]).pathname).toBe('/api/v1/auth/session');
    expect(API_BASE_URL).toBe('/api/v1');
  });
});
