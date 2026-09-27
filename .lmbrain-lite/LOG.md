# Log

One line per action. Format: `timestamp | by:<who> | <target or -> | <kind> | <message>`.

2026-09-27T23:22 | by:lead | - | note | Avviata analisi pre-sviluppo di GitStack (mini GitHub on-prem con visione "piccolo cloud privato"); progetto vuoto, nessuna milestone.
2026-09-27T23:23 | by:lead | - | decision | Decisione D1: GitStack è un prodotto distribuito a terzi che lo installano on-prem; installer, aggiornamenti e documentazione sono requisiti dal giorno uno.
2026-09-27T23:23 | by:lead | - | decision | Decisione D2: GitStack gira su k3s incluso nell'installer (un comando, da 1 a N nodi, base per l'hosting futuro di app in container).
2026-09-27T23:24 | by:lead | - | decision | Decisione D3: backend in Go.
2026-09-27T23:24 | by:lead | - | decision | Decisione D4: pochi servizi grossi con confini netti (gateway/identità, Git, API core, web UI, bus eventi).
2026-09-27T23:25 | by:lead | - | decision | Decisione D5: identità nostra in Go (utenti locali, SSH, token con scope) più login esterno OIDC; LDAP rimandato.
2026-09-27T23:25 | by:lead | - | decision | Decisione D6: PostgreSQL incluso (o esterno del cliente) per i dati strutturati; repo Git su volume persistente.
2026-09-27T23:26 | by:lead | - | decision | Decisione D7: web UI React + TypeScript, principio API-first (UI e agenti usano la stessa API pubblica).
2026-09-27T23:26 | by:lead | - | decision | Decisione D8: API REST+OpenAPI; v1 con CLI gs e skills per agenti, MCP nella release successiva.
2026-09-27T23:27 | by:lead | - | decision | Decisione D9: codice v1 = push/pull HTTPS+SSH, browser web, organizzazioni e permessi; Pull Request in v1.1 con modello dati pronto.
2026-09-27T23:27 | by:lead | - | decision | Decisione D10: issues v1 con etichette, milestone, collegamenti ai commit, notifiche e webhook; Kanban rimandato.
2026-09-27T23:28 | by:lead | - | decision | Decisione D11: bus eventi tra servizi con NATS JetStream.
2026-09-27T23:29 | by:lead | - | decision | Decisione D12: GitStack interamente open source.
2026-09-27T23:29 | by:lead | - | decision | Decisione D13: licenza AGPL-3.0; aperta la valutazione di una licenza permissiva per CLI e skills.
2026-09-27T23:32 | by:lead | - | decision | Decisione D14: installazione su Linux e Windows via WSL2 (rischio: fragilità WSL2 in produzione da gestire).
2026-09-27T23:32 | by:lead | - | decision | Decisione D15: v1 senza CI/registry/deploy, ma con eventi, permessi e API su risorse generiche per agganciarli dopo.
2026-09-27T23:33 | by:lead | - | decision | Decisione D16: sviluppo operatore + agenti AI, milestone piccole; prima milestone = scheletro end-to-end.
2026-09-27T23:33 | by:lead | - | note | Scritta la knowledge di base: PROJECT.md, knowledge/vision.md, knowledge/architecture.md, knowledge/decisions.md (D1–D16, default e punti aperti).
2026-09-27T23:36 | by:lead | - | decision | Decisioni D17–D19: Apache-2.0 per CLI/skills, aggiornamenti con gitstack upgrade (backup+rollback), backup/restore coerente e pianificato; nessun punto aperto.
2026-09-27T23:36 | by:lead | M-01 | milestone | created 'Scheletro che cammina' with 11 tasks (proposed)
2026-09-27T23:36 | by:lead | M-02 | milestone | created 'Identità, organizzazioni e permessi' with 9 tasks (proposed)
2026-09-27T23:36 | by:lead | M-03 | milestone | created 'Hosting dei repository Git' with 8 tasks (proposed)
2026-09-27T23:36 | by:lead | M-04 | milestone | created 'Browser del codice' with 7 tasks (proposed)
2026-09-27T23:36 | by:lead | M-05 | milestone | created 'Issues' with 8 tasks (proposed)
2026-09-27T23:36 | by:lead | M-06 | milestone | created 'Collegamenti, notifiche e webhook' with 7 tasks (proposed)
2026-09-27T23:36 | by:lead | M-07 | milestone | created 'CLI gs e skills per agenti' with 9 tasks (proposed)
2026-09-27T23:36 | by:lead | M-08 | milestone | created 'Installazione, aggiornamenti e backup' with 9 tasks (proposed)
2026-09-27T23:36 | by:lead | M-09 | milestone | created 'Rilascio v1.0' with 6 tasks (proposed)
2026-09-27T23:37 | by:lead | - | decision | Proposta roadmap v1: milestone M-01…M-09 in stato proposed, in attesa di approvazione dell'operatore.
2026-09-27T23:37 | by:operator | M-01 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-02 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-03 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-04 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-05 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-06 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-07 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-08 | status | proposed -> approved
2026-09-27T23:37 | by:operator | M-09 | status | proposed -> approved
2026-09-27T23:39 | by:lead | M-01 | status | approved -> active: Avvio M-01: prima styleguide e mockup, poi sviluppo.
2026-09-27T23:39 | by:lead | M-01/T-12 | task | added 'Styleguide di prodotto e mockup delle schermate v1 in design/'
2026-09-27T23:52 | by:lead | M-01/T-12 | task | done: Styleguide e 16 mockup in design/, verificati nel browser in tema chiaro e scuro; riferiti nelle Notes di M-01…M-08. — Styleguide di prodotto e mockup delle schermate v1 in design/
2026-09-27T23:52 | by:lead | M-01/T-12 | decision | Creati design/styleguide e design/mockups-v1 (16 schermate), knowledge/design-system.md e decisione D20 stile "console"; UI in inglese con i18n come default.
2026-09-27T23:57 | by:lead | M-01/T-12 | decision | Palette cambiata da teal ad "Aurora" (viola + sfumatura viola→magenta→arancio) su richiesta dell'operatore, per distinguersi da LMBrain; styleguide, mockup, D20 e design-system aggiornati e verificati nel browser.
2026-09-27T23:59 | by:lead | M-01 | commit | Commit c268868 pushato su origin/main: analisi, decisioni D1–D20, roadmap M-01…M-09, styleguide e mockup Aurora. Prossimo passo: M-01/T-01.
