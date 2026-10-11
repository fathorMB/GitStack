---
{"depends_on":[],"id":"TOP-0b04024c-6f6b-4d35-b790-97d7591d6c08","knowledge":["DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d"],"reopen_reason":"Tutte le domande del tema risolte e confermate dall'operatore (2026-10-12).","schema_version":1,"state":"consolidated","title":"v2 — CI e registry container: regole di prodotto","updated":"2026-10-11T22:57:32.570559700+00:00"}
---


# v2 — CI e registry container: regole di prodotto

## Expected learning

Regole di prodotto per la v2 "Build" (CI, registry, release), analizzate in anticipo il 2026-10-12; realizzazione nella v2 dopo v1.0 e v1.1, come già previsto.

## Scelte confermate dall'operatore (2026-10-12)

Testo completo in [[knowledge/topics/ci-registry-release]].

- **CI1** workflow compatibili con GitHub Actions, anche da `.github/workflows/`.
- **CI2** runner integrato isolato con barriere fisse, runner esterni facoltativi.
- **CI3** eventi push, pull_request, workflow_dispatch, schedule; niente CI su archiviati, spenta sui mirror in pull.
- **CI4** segreti e variabili di repo e organizzazione; token del job mai oltre i permessi di chi avvia.
- **CI5** log 90 giorni, artefatti 30 giorni, cache 10 GB per repo; disco o S3; artefatti e cache fuori dai backup.
- **CI6** stati dei commit, scheda Actions, notifiche su fallimento e ritorno al verde, `gs run` e `gs workflow`.
- **CI7** limiti automatici in base all'hardware, priorità a GitStack, coda equa.
- **RG1** immagini OCI legate al repo, con permessi e visibilità del repo.
- **RG2** pulizia automatica, tag protetti, quote; immagini escluse di default dai backup (scelta C dell'operatore: si ricreano dalla CI).
- **REL1** release come GitHub con `gs release`; file allegati esclusi di default dai backup.
- **Ordine:** v2-a CI, v2-b Registry, v2-c Release.

## Open questions

Nessuna nel tema. Resta aperta, nel tema del rilascio, la soluzione ponte per la CI dei branch di GitStack dopo la tappa 2.

## Consolidation summary

Consolidato il 2026-10-12 con conferma dell'operatore. La v2 si realizza in tre passi utilizzabili da soli: CI (che restituisce la CI ai branch di GitStack dopo la tappa 2), registry, release. Regole in [[knowledge/topics/ci-registry-release]]; obiettivi v2-a, v2-b, v2-c in roadmap.

