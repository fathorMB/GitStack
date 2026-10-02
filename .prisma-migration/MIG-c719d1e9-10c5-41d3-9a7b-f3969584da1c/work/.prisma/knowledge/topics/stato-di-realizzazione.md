---
{"area":"delivery","id":"DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/ROADMAP.md","source_id":"SRC-e44cf26d-7f3c-466b-a405-558022e98ca5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"},{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"}],"tags":["stato","milestone","v1"],"title":"Stato di realizzazione della v1","updated":"2026-09-30T21:13:45.441327600+00:00"}
---

# Stato di realizzazione della v1

## Context

Il cervello Lite si è fermato al 2026-09-27 (M-01 1/12). Lo stato sotto è stato **ricostruito dal repo** (README e storia dei commit fino a `af42239`, GIT-60) il 2026-09-30 e scelto dall'operatore come riferimento, al posto dell'indicazione iniziale "tutte completate".

## Confirmed decisions

| Milestone | Stato | Prove |
|---|---|---|
| M-01 Scheletro che cammina | **completata** | monorepo e licenze, CI, OpenAPI e client generati, gateway, core con migrazioni, `pkg/events`, web UI, chart Helm, installer v0, ambiente k3d, VM e test e2e (GIT-1…GIT-28) |
| M-02 Identità, organizzazioni e permessi | **in corso** | realizzati: utenti e password, admin al primo avvio, sessioni, token con scope, chiavi SSH, OIDC, organizzazioni e team, autenticazione centralizzata nel gateway, UI di login/profilo/token/org (GIT-29…GIT-60). Manca: modello dei permessi sulle risorse (GIT-38, `/internal/permissions/check` = 501) |
| M-03 Hosting Git | da fare | `services/git` solo scheletro |
| M-04 Browser del codice | da fare | voci "Soon" nella UI |
| M-05 Issues | da fare | — |
| M-06 Collegamenti, notifiche, webhook | da fare | — |
| M-07 CLI `gs` e skills | da fare | `cli/` scheletro, `skills/` vuota |
| M-08 Installazione, upgrade, backup | da fare | solo installer v0 Ubuntu, HTTP |
| M-09 Rilascio v1.0 | da fare | nessun tag di versione |

Il dettaglio di task e avanzamento resta nel tracker GalaxyLab, non in Prisma.

## Open questions

- Da riallineare quando M-02 si chiude.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/sviluppo-e-qualita]]

