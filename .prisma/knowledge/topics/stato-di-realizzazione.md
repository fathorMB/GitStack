---
{"area":"delivery","id":"DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","related":[],"reopen_reason":"Riallineamento al codice di main al commit de88a0e (2026-10-05).","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/ROADMAP.md","source_id":"SRC-e44cf26d-7f3c-466b-a405-558022e98ca5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"},{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"}],"tags":["stato","milestone","v1"],"title":"Stato di realizzazione della v1","updated":"2026-10-05T09:10:00+00:00"}
---

# Stato di realizzazione della v1

## Context

Stato **ricostruito dal repo** (storia dei commit e codice) e riallineato il 2026-10-05 al commit `de88a0e` su `main`. La prima ricostruzione (2026-09-30, fino a `af42239`) era stata scelta dall'operatore come riferimento al posto dell'indicazione "tutte completate".

## Confirmed decisions

| Milestone | Stato | Prove |
|---|---|---|
| M-01 Scheletro che cammina | **completata** | monorepo e licenze, CI, OpenAPI e client generati, gateway, core con migrazioni, `pkg/events`, web UI, chart Helm, installer v0, ambiente k3d, VM e test e2e (GIT-1…GIT-28) |
| M-02 Identità, organizzazioni e permessi | **quasi completata** | realizzati: utenti e password, admin al primo avvio, sessioni, token con scope, chiavi SSH, OIDC, organizzazioni e team (GIT-37), autenticazione centralizzata nel gateway, modello dei permessi su risorse con verifica centralizzata (GIT-38), grant admin automatico a chi crea una risorsa (GIT-61), elenco risorse filtrato per permesso (GIT-62), test di sicurezza sullo stack (GIT-42), utenti agent senza password, UI di login/profilo/token/org (GIT-29…GIT-62). Manca: gestione dei token degli utenti agent da parte dell'admin (P5) e schermata Admin · Agents (mockup 17) |
| M-03 Hosting Git | **in corso** | realizzati: contratto API dei repo e schema `core` predisposto per le PR (GIT-63), validazione dei nomi R11 con spazio di nomi unico e nomi riservati (GIT-64), servizio git con storage dei repo bare e API interna di creazione/cestino/ripristino (GIT-66), modelli di contenuto iniziale (GIT-69), UI elenco, nuovo repo e repo vuoto (GIT-75). Manca: API pubblica dei repo in core (oggi 501, GIT-67), push/pull HTTPS e SSH, controllo permessi sulle operazioni Git, evento `git.push`, limiti (R6), protezione (R9) e archiviazione (R10), test end-to-end con client git reale |
| M-04 Browser del codice | da fare | voci "Soon" nella UI |
| M-05 Issues | da fare | — |
| M-06 Collegamenti, notifiche, webhook | da fare | — |
| M-07 CLI `gs` e skills | da fare | `cli/` scheletro, `skills/` vuota |
| M-08 Installazione, upgrade, backup | da fare | solo installer v0 Ubuntu, HTTP |
| M-09 Rilascio v1.0 | da fare | nessun tag di versione |

Il dettaglio di task e avanzamento resta nel tracker GalaxyLab, non in Prisma.

## Open questions

- Da riallineare quando M-02 si chiude e a ogni avanzamento rilevante di M-03.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/sviluppo-e-qualita]]

