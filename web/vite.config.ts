/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Configurazione Vite per web/ (scheletro React+TS, M-01 T-07).
//
// - `server.proxy`: solo per `pnpm dev` in locale. La SPA chiama sempre
//   l'URL relativo `/api/v1/...` (mai un host di servizio diverso dal
//   gateway, D7): qui si simula quel che fa l'Ingress in produzione (e il
//   nginx di `web/Dockerfile`): si toglie solo `/api`, il gateway locale
//   su :8080 riceve `/v1/...`.
//   Niente `changeOrigin`: l'header Host resta quello del dev server, come
//   l'`Origin` del browser; il gateway rifiuta (403 cross_site_request) un POST
//   con cookie la cui `Origin` non coincide con l'host (F16).
// - `test`: Vitest con ambiente DOM (jsdom), per i test di componente React.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
  test: {
    environment: 'jsdom',
    // Gli smoke Playwright di e2e/ (GIT-152) non sono test Vitest: `pnpm e2e`.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    // I test asincroni di jsdom superano i 5 s quando la macchina è carica (GIT-318).
    testTimeout: 15000,
    hookTimeout: 15000,
  },
});
