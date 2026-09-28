# web/

Interfaccia web di GitStack: React + TypeScript, SPA (Vite). Parla solo con l'API pubblica esposta dal `gateway`, la stessa usata da CLI, skills e (in futuro) server MCP — principio API-first (vedi `.lmbrain-lite/knowledge/decisions.md`, D7).

Stato: scheletro (M-01 T-07). Shell dell'app (sidebar + topbar, `src/layout/`) coerente con `design/styleguide/` e `design/mockups-v1/` (token in `src/styles/tokens.css`, copiati dalla styleguide, non ricopiati a mano campo per campo altrove). Una pagina (`src/pages/ResourcesPage.tsx`) elenca e crea la risorsa generica di prova (D15) usando solo il client TypeScript generato da T-03 (`@gitstack/api-client`, vedi `client/ts/`) e passando dal gateway, con base URL relativa (`/api`, mai un host di servizio diverso). Le altre voci di navigazione (repository, issue...) sono ancora disattivate ("Soon"): non esistono ancora come funzionalità di prodotto.

## Sviluppo

```sh
pnpm install
pnpm dev        # http://localhost:5173, proxy /api -> gateway locale su :8080 (vedi vite.config.ts)
pnpm lint
pnpm typecheck
pnpm test
pnpm build      # tsc --noEmit + vite build, output statico in dist/
```

## Dipendenza da client/ts

`@gitstack/api-client` è dichiarata come `"link:../client/ts"` in `package.json`: non è pubblicata su un registry npm (vedi `client/README.md`), pnpm la collega direttamente alla cartella sorgente. Per questo `web/Dockerfile` costruisce con il contesto alla radice del monorepo, non `web/` da sola (serve anche `client/ts/`).

## Package manager

`pnpm` (vedi `package.json` → `packageManager`, e `pnpm-lock.yaml` per le versioni bloccate). Un solo package manager per tutto il frontend, così le build sono riproducibili.

## Container

`web/Dockerfile`: build statica (stage Node) servita da `nginxinc/nginx-unprivileged` (stage runtime, utente non privilegiato `nginx`, porta 8080). Il template `deploy/nginx.conf.template` inoltra `/api/*` al gateway (`GATEWAY_UPSTREAM`, sovrascritto dal chart Helm di GIT-8) riscrivendolo su `/v1/*`, e fa da fallback SPA (`try_files ... /index.html`) per tutto il resto.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice e la motivazione nel README principale).
