---
{"area":"design","id":"DOC-ef8572eb-11a9-4ab0-ac04-ff006b41bdc4","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/design-system.md","source_id":"SRC-097a8c99-5cbb-4109-9eee-9b8df512c2c9"}],"tags":["design","ui","token"],"title":"Design system Aurora","updated":"2026-09-30T21:13:45.437151800+00:00"}
---

# Design system Aurora

## Context

Stile "console" con palette **Aurora** (decisione D20). Riferimento visivo: `design/styleguide/index.html`; 16 schermate v1 navigabili in `design/mockups-v1/index.html` (login, home, repository, nuovo repo, notifiche, repo vuoto, codice, file, commit, diff, webhook, lista e dettaglio issue, token e chiavi SSH, organizzazione e team, admin di sistema). Dati finti: organizzazione "Acme", utenti mrossi, lbianchi, gverdi, fneri, agente build-agent.

## Confirmed decisions

- **Token, mai valori fissi.** Colori, raggi e ombre sono variabili CSS (`--accent`, `--surface`, `--text-muted`…). In `web/src/styles/tokens.css` sono **generati** dalla styleguide con `web/scripts/sync-tokens.mjs` (controllo `check-tokens` in CI), mai copiati a mano.
- **Accento:** viola `#6a47f5` (chiaro) / `#7c5cff` (scuro). Sidebar `#0e0b16` in entrambi i temi.
- **Sfumatura Aurora** (`#7c5cff → #e0469b → #ff8a3d`): solo logo, voce attiva della sidebar, avatar degli agenti, pagina di login. Mai su bottoni o contenuti.
- **Stati:** aperta = verde, chiusa = magenta, agente = arancio; sempre colore + icona + testo.
- **Font:** Inter (UI), JetBrains Mono (codice, hash, branch, comandi), inclusi nell'immagine per installazioni offline.
- **Densità:** testo 14 px, righe 36–40 px, griglia 4 px, raggi 4/6/10 px.
- **Layout:** sidebar 232 px (gruppi Workspace, Platform "Soon", Administration) + topbar 48–52 px + contenuto max 1280 px.
- **Agenti:** avatar arancio → magenta con robot, badge `agent`, sempre "per conto di" chi possiede il token; etichetta `agent-ready`.
- **Lingua UI:** inglese di default, i18n dalla v1 (italiano incluso).
- **Librerie previste:** Radix UI Primitives (MIT), lucide-react (ISC), Shiki (MIT), compatibili con AGPL-3.0.

## Superseded

- Palette teal iniziale, scartata perché troppo simile a LMBrain.

## Open questions

- `web/scripts/sync-tokens.mjs` legge la styleguide da `.lmbrain-lite/design/styleguide/index.html`. Dopo la migrazione la copia di riferimento sarà `.prisma/design/styleguide/index.html`: va deciso se spostare lì la fonte dei token (modifica di codice, da affidare a una sessione di coding).

## Related topics

- [[knowledge/topics/decisioni]]

