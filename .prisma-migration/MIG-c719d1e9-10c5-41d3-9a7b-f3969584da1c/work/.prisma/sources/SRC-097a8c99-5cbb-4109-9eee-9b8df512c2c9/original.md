---
title: Design system
updated: 2026-09-27
---

# Design system

Stile "console" con palette **Aurora**: barra laterale quasi nera fissa, accento viola elettrico, sfumatura viola → magenta → arancio per il marchio, densità da strumento, tema chiaro e scuro. Riferimento visivo completo in `design/styleguide/index.html`; schermate in `design/mockups-v1/index.html`. Decisioni di prodotto in [[decisions]].

## Regole chiave

- **Token, mai valori fissi.** Colori, raggi e ombre sono variabili CSS (`--accent`, `--surface`, `--text-muted`…). Nel codice si copiano in `web/src/styles/tokens.css`, identiche alla styleguide.
- **Accento:** viola `#6a47f5` (chiaro) / `#7c5cff` (scuro). Sidebar `#0e0b16` in entrambi i temi.
- **Sfumatura Aurora** (`--aurora`: `#7c5cff → #e0469b → #ff8a3d`): solo per logo, voce attiva della sidebar, avatar degli agenti, pagina di login. Mai sui bottoni o sui contenuti.
- **Stati:** aperta = verde, chiusa = magenta (non viola, per non confondersi con l'accento), agente = arancio; sempre colore + icona + testo.
- **Font:** Inter (UI), JetBrains Mono (codice, hash, branch, comandi). Inclusi nell'immagine della UI per le installazioni offline.
- **Densità:** testo 14 px, righe 36–40 px, griglia 4 px, raggi 4/6/10 px.
- **Layout:** sidebar 232 px (gruppi Workspace, Platform "Soon", Administration) + topbar 48–52 px + contenuto max 1280 px.
- **Agenti:** avatar con sfumatura arancio → magenta e robot, badge `agent`, sempre "per conto di" chi possiede il token; etichetta `agent-ready`.
- **Lingua UI:** inglese di default, i18n pronto dalla v1 (italiano incluso).

## Librerie previste

Radix UI Primitives (MIT) per i componenti accessibili, lucide-react (ISC) per le icone, Shiki (MIT) per l'evidenziazione del codice. Tutte compatibili con AGPL-3.0.
