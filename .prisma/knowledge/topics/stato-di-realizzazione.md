---
{"area":"delivery","id":"DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","related":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d"],"reopen_reason":"Riallineamento al codice di main al commit e708707 (2026-10-12) e alle indicazioni del CEO, confermato dall'operatore.","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/ROADMAP.md","source_id":"SRC-e44cf26d-7f3c-466b-a405-558022e98ca5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"},{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"}],"tags":["stato","milestone","v1"],"title":"Stato di realizzazione della v1","updated":"2026-10-11T22:21:10.963834+00:00"}
---


# Stato di realizzazione della v1

## Context

Stato **ricostruito dal repo** (`docs/rules-coverage.md`, `docs/operations.md`, `deploy/`) e dalle indicazioni del CEO, riallineato il 2026-10-12 al commit `e708707` su `main` e confermato dall'operatore. Allineamenti precedenti: `af42239` (2026-09-30), `de88a0e` e `1587133` (2026-10-05). "Completata" significa che il codice copre tutto ciò che la milestone prevede e la sua famiglia di regole è coperta da test; la chiusura formale degli item resta nel tracker GalaxyLab.

## Confirmed decisions

| Milestone | Stato | Prove |
|---|---|---|
| M-01 Scheletro che cammina | **completata** | GIT-1…GIT-28 |
| M-02 Identità, organizzazioni e permessi | **completata** | GIT-29…GIT-62, GIT-78, GIT-79, GIT-95; famiglia P coperta, P7 compresa la parte `gs` |
| M-03 Hosting Git | **completata** | GIT-65…GIT-77, GIT-99, GIT-100; famiglia R coperta salvo dettagli di test (R1, R3) e la parte installer di R7, che passa a M-08 |
| M-04 Browser del codice | **completata** | GIT-80…GIT-94, GIT-116…GIT-121; famiglia B coperta, B4 e B5 comprese le parti `gs` |
| M-05 Issues | **completata** | GIT-101…GIT-113; famiglia I1–I11 coperta, compresa la sola lettura sui repo archiviati (R10) |
| M-06 Collegamenti, notifiche, webhook | **completata** | famiglia C1–C9 coperta (collegamenti e chiusura via commit, notifiche, email, webhook firmati, protezione SSRF con `pkg/egress`) |
| M-07 CLI `gs` e skills | **completata** | famiglia G1–G8 coperta; distribuzione da `/downloads`; guida alla prova reale di un agente (GIT-174, `docs/agent-trial.md`) |
| M-08 Installazione, upgrade, backup | **in corso** | fatto: preflight hardware (N1) e nome host (N6, `gitstack config set host`), HTTPS con CA interna, Let's Encrypt o certificato del cliente e avviso per HTTP (N5, parziale nei test), comando `gitstack` con `status`, `backup` (anche cifrato), `restore`, `upgrade` con backup preventivo e rollback, timer di backup giornaliero locale, guida operativa (`docs/operations.md`) |
| M-09 Rilascio v1.0 | **in corso** | fatto: tabella regole→test con controllo in CI (V1, GIT-115), scansioni di sicurezza in CI (V2, GIT-114), mirror in push verso GitHub (V8-mirror, GIT-179). Nessun tag di versione |

### Cosa resta della v1 (indicazioni del CEO, 2026-10-12)

1. **Resto di M-08:** Ubuntu 22.04 e Pop!_OS 22.04/24.04 (N2; oggi l'installer accetta solo Ubuntu 24.04); Windows via WSL2 di prova (W1, W2); mirror e proxy aziendali (N4; oggi solo `--image-registry`); Postgres esterno supportato dall'installer (oggi solo via `--set postgres.enabled=false` del chart); backup su S3 (D19; oggi solo locale più `rsync`); verifica delle firme cosign in `install.sh` e `gitstack upgrade` (V5); test in CI di installazione, aggiornamento e ripristino sui sistemi supportati (N2; oggi le prove reali girano sulla VM lanciata dal board).
2. **M-09**, comprese la **migrazione vera del codice su homehub** ("fase 3", legata all'item GalaxyLab GL-31) e le altre parti di V1–V8 non ancora fatte (revisione di sicurezza, test di fumo, versioni e `SECURITY.md`, pipeline firmata con SBOM, sito di documentazione, «Sincronizza ora» e stato del mirror nella UI, GIT-180).

Il dettaglio di task e avanzamento resta nel tracker GalaxyLab, non in Prisma.

## Open questions

- Cosa sono le fasi del dogfooding (in particolare la "fase 3") e cosa chiede GL-31: Prisma conosce solo V7 ("passaggio graduale"). Da chiarire con l'operatore.
- `deploy/README.md` e `docs/operations.md` dicono ancora "WSL2 e altre distribuzioni arrivano con M-08" e citano `.lmbrain-lite`: da aggiornare quando M-08 chiude.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/rilascio-v1]]

