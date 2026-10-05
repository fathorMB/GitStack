import { defineConfig } from '@playwright/test';

// Smoke e2e del browser (GIT-152): fuori da Vitest (vite.config.ts non include
// e2e/) e fuori da `pnpm test`. Si lancia con `pnpm e2e`.
// E2E_CHROMIUM_ARGS: argomenti extra di Chromium, vuota = nessuno. Un array JSON
// ('["--host-resolver-rules=MAP gitstack.test 127.0.0.1"]', necessario se un
// argomento contiene spazi) oppure argomenti separati da spazi.
function chromiumArgs(): string[] {
  const raw = (process.env.E2E_CHROMIUM_ARGS ?? '').trim();
  if (raw === '') return [];
  if (raw.startsWith('[')) return (JSON.parse(raw) as unknown[]).map(String);
  return raw.split(/\s+/);
}

// E2E_BASE_URL: indirizzo dell'Ingress (CI k3d: http://localhost:8080; VM:
// http://<ip della VM>). Nessun default silenzioso su un host diverso.
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  outputDir: 'e2e-results',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:8080',
    headless: true,
    launchOptions: { args: chromiumArgs() },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
