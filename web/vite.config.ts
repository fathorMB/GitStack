/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Configurazione Vite per web/ (scheletro React+TS, M-01 T-07).
//
// - `server.proxy`: solo per `pnpm dev` in locale. La SPA chiama sempre
//   l'URL relativo `/api/...` (mai un host di servizio diverso dal
//   gateway, D7): qui si simula quel che fa `web/Dockerfile` in produzione
//   (nginx che inoltra /api/ al gateway su /v1/), puntando a un gateway
//   locale su :8080.
// - `test`: Vitest con ambiente DOM (jsdom), per i test di componente React.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, '/v1'),
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
  },
});
