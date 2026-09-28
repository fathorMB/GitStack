# web/

Interfaccia web di GitStack: React + TypeScript, SPA. Parla solo con l'API pubblica esposta dal `gateway`, la stessa usata da CLI, skills e (in futuro) server MCP — principio API-first (vedi `.lmbrain-lite/knowledge/decisions.md`, D7).

Stato: scheletro di package. Il progetto React/Vite vero e proprio (layout, routing, chiamata di prova all'API) arriva con M-01 T-07.

## Package manager

`pnpm` (vedi `package.json` → `packageManager`, e `pnpm-lock.yaml` per le versioni bloccate). Un solo package manager per tutto il frontend, così le build sono riproducibili.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice e la motivazione nel README principale).
