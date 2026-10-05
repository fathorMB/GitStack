---
{"area":"delivery","id":"DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","related":[],"reopen_reason":"Riallineamento al codice di main al commit 1587133 (2026-10-05), richiesto dall'operatore.","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/ROADMAP.md","source_id":"SRC-e44cf26d-7f3c-466b-a405-558022e98ca5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"},{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"}],"tags":["stato","milestone","v1"],"title":"Stato di realizzazione della v1","updated":"2026-10-05T19:50:04.895621+00:00"}
---


# Stato di realizzazione della v1

## Context

Stato **ricostruito dal repo** (storia dei commit, codice e `docs/rules-coverage.md`) e riallineato il 2026-10-05 al commit `1587133` su `main`. Allineamenti precedenti: `af42239` (2026-09-30, scelto dall'operatore come riferimento al posto di "tutte completate") e `de88a0e` (2026-10-05 mattina). "Completata" qui significa che i commit coprono tutto ciò che la milestone prevede; la chiusura formale degli item resta nel tracker GalaxyLab.

## Confirmed decisions

| Milestone | Stato | Prove |
|---|---|---|
| M-01 Scheletro che cammina | **completata** | monorepo e licenze, CI, OpenAPI e client generati, gateway, core con migrazioni, `pkg/events`, web UI, chart Helm, installer v0, ambiente k3d, VM e test e2e (GIT-1…GIT-28) |
| M-02 Identità, organizzazioni e permessi | **completata** | tutto quanto già realizzato (GIT-29…GIT-62) più: token degli utenti agent gestiti dall'admin, P5 (GIT-78); UI Admin · Agents, mockup 17 (GIT-79); API dell'accesso effettivo di un utente (GIT-95). Regole P1–P6 coperte da test; P7 parziale solo per la parte `gs` |
| M-03 Hosting Git | **completata** | permessi da owner e visibilità (GIT-65), API pubblica dei repo (GIT-67), eliminazione recuperabile 7 giorni (GIT-68), push/pull HTTPS con token e permessi (GIT-70), server SSH sulla 2222 (GIT-71, GIT-99, GIT-100), regole al push R6/R9/R10 (GIT-72), evento `git.push` su NATS (GIT-73, GIT-97), deploy del servizio git (GIT-74), UI impostazioni e repo eliminati (GIT-76), test e2e con client git reale in CI e su VM (GIT-77). Regole R parziali solo per parti che dipendono da `gs`, dall'installer o dalle issues |
| M-04 Browser del codice | **completata** | contratto delle letture (GIT-80, GIT-91), servizio git: albero, file, raw, branch, tag, archivi, commit, diff, blame, lingue (GIT-81…GIT-83), letture con permessi (GIT-84), Markdown sicuro (GIT-85, GIT-90, GIT-92), UI Code, file, History, Blame, Raw, commit e diff, Tags e Search code (GIT-86, GIT-87, GIT-88, GIT-93, GIT-94), test e2e e su VM (GIT-89, GIT-116, GIT-121), correzioni (GIT-117, GIT-120). Regole B1, B2, B3, B6, B7 coperte; B4 e B5 parziali solo per la parte `gs` |
| M-05 Issues | **in corso** | realizzati: contratto API e schema (GIT-101), sintassi di ricerca I10 (GIT-102), issues in core con numerazione, stato e permessi (GIT-103), commenti, cronologia, blocco ed eliminazione tracciata (GIT-104), etichette, milestone e assegnatari (GIT-105), ricerca nel repo e globale (GIT-106), allegati fino a 10 MB (GIT-107), UI lista issues con filtri, mockup 12 (GIT-109). Mancano, dal codice: UI di dettaglio e di nuova issue, issues in sola lettura sui repo archiviati (R10) e la tabella regole→test I1–I11 (GIT-113) |
| M-06 Collegamenti, notifiche, webhook | da fare | — |
| M-07 CLI `gs` e skills | da fare | `cli/` scheletro, `skills/` vuota |
| M-08 Installazione, upgrade, backup | da fare | solo installer v0 Ubuntu, HTTP |
| M-09 Rilascio v1.0 | da fare, con due parti anticipate | V1 anticipato: tabella regole→test in `docs/rules-coverage.md` con controllo in CI (GIT-115); V2 anticipato: scansioni di sicurezza in CI, segnalanti su main e bloccanti sui tag (GIT-114). Nessun tag di versione |

Lavoro di qualità trasversale nel periodo: correzioni di CI e test instabili (GIT-96, GIT-98, GIT-118, GIT-119, GIT-122, GIT-123).

Il dettaglio di task e avanzamento resta nel tracker GalaxyLab, non in Prisma.

## Open questions

- La roadmap prodotto mette ancora M-02 in "now" e M-03/M-04 in "next": va riallineata con una proposta.
- In `docs/rules-coverage.md` la CLI `gs` è attribuita a "M-06", mentre in Prisma è M-07: probabile svista da segnalare ai coding agent.
- Da riallineare alla chiusura di M-05.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/rilascio-v1]]

