---
{"area":"design","id":"DOC-ef8572eb-11a9-4ab0-ac04-ff006b41bdc4","related":["TOP-4a11694d-e261-4934-bafb-f17ccc6d1726"],"reopen_reason":"Conflitto risolto dall'operatore il 2026-09-30: nessun responsabile umano per gli agent; regola \"per conto di\" superata.","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/design-system.md","source_id":"SRC-097a8c99-5cbb-4109-9eee-9b8df512c2c9"}],"tags":["design","ui","token"],"title":"Design system Aurora","updated":"2026-10-05T10:00:00+00:00"}
---


# Design system Aurora

## Context

Stile "console" con palette **Aurora** (decisione D20). Riferimento visivo: `design/styleguide/index.html`; 24 schermate v1 navigabili in `design/mockups-v1/index.html` (login, home, repository, nuovo repo, notifiche, repo vuoto, codice, file, commit, diff, blame, tag, webhook, lista, dettaglio e nuova issue, token e chiavi SSH, download di CLI e skills, preferenze di notifica, organizzazione e team, admin di sistema, admin agent, impostazioni generali del repo, repo eliminati). Dati finti: organizzazione "Acme", utenti mrossi, lbianchi, gverdi, fneri, agenti build-agent, review-agent, docs-agent.

Aggiornamento 2026-09-30: mockup allineati alle regole sui permessi (P2–P5): visibilità solo Private (default) e Internal; schermata 14 con il catalogo scope reale e scadenza obbligatoria; nuova schermata 17 **Admin · Agents** (account agent senza password, token creati e revocati dall'admin, accesso effettivo da grant, team e visibilità).

## Confirmed decisions

- **Token, mai valori fissi.** Colori, raggi e ombre sono variabili CSS (`--accent`, `--surface`, `--text-muted`…). In `web/src/styles/tokens.css` sono **generati** dalla styleguide con `web/scripts/sync-tokens.mjs` (controllo `check-tokens` in CI), mai copiati a mano.
- **Accento:** viola `#6a47f5` (chiaro) / `#7c5cff` (scuro). Sidebar `#0e0b16` in entrambi i temi.
- **Sfumatura Aurora** (`#7c5cff → #e0469b → #ff8a3d`): solo logo, voce attiva della sidebar, avatar degli agenti, pagina di login. Mai su bottoni o contenuti.
- **Stati:** aperta = verde, chiusa = magenta, agente = arancio; sempre colore + icona + testo.
- **Font:** Inter (UI), JetBrains Mono (codice, hash, branch, comandi), inclusi nell'immagine per installazioni offline.
- **Densità:** testo 14 px, righe 36–40 px, griglia 4 px, raggi 4/6/10 px.
- **Layout:** sidebar 232 px (gruppi Workspace, Platform "Soon", Administration con Organization, Agents, System) + topbar 48–52 px + contenuto max 1280 px.
- **Agenti:** avatar arancio → magenta con robot, badge `agent`, etichetta `agent-ready`. Un agent è un **utente a sé** (P4, P5): le sue azioni sono firmate dal suo account e, dove serve, dal nome del token usato; **nessun responsabile umano** associato (scelta dell'operatore, 2026-09-30). Chi gestisce gli agent è l'amministratore dell'installazione.
- **Lingua UI:** inglese di default, i18n dalla v1 (italiano incluso).
- **Librerie previste:** Radix UI Primitives (MIT), lucide-react (ISC), Shiki (MIT), compatibili con AGPL-3.0.

## Superseded

- Palette teal iniziale, scartata perché troppo simile a LMBrain.
- Regola "agente mostrato sempre *per conto di* chi possiede il token" (design originale del 2026-09-27): **superata** il 2026-09-30, perché con P4/P5 l'agent ha account e token propri.

## Open questions

- `web/scripts/sync-tokens.mjs` legge la styleguide da `.lmbrain-lite/design/styleguide/index.html`. Dopo la migrazione la copia di riferimento sarà `.prisma/design/styleguide/index.html`: va deciso se spostare lì la fonte dei token (modifica di codice, da affidare a una sessione di coding).

## Related topics

- [[knowledge/topics/decisioni]]
- [[knowledge/topics/identita-e-sicurezza]]

