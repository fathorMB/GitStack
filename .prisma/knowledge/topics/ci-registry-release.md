---
{"area":"requirements","id":"DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","related":["TOP-0b04024c-6f6b-4d35-b790-97d7591d6c08","OBJ-397d65b0-f4a8-4a4c-b965-08d5e41c8be0","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","DOC-04fbc589-a144-403b-99e0-13e541fac16a","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7"],"reopen_reason":"Regole CI1–CI7, RG1–RG2, REL1 della v2 confermate dall'operatore il 2026-10-12.","schema_version":1,"sources":[],"tags":["v2","ci","registry","release","build"],"title":"v2 — CI, registry e release: regole di prodotto","updated":"2026-10-11T22:52:36.681295+00:00"}
---


# v2 — CI, registry e release: regole di prodotto

## Context

Regole confermate dall'operatore il 2026-10-12 nel tema di analisi "v2 — CI e registry container". Analisi fatta in anticipo; la realizzazione resta nella **v2**, dopo v1.0 e v1.1, in tre passi: **v2-a CI**, **v2-b Registry**, **v2-c Release**. Si appoggiano a [[knowledge/topics/pull-request]] (PR1, PR4, PR9), [[knowledge/topics/collegamenti-notifiche-webhook]], [[knowledge/topics/cli-gs-skills]], [[knowledge/topics/identita-e-sicurezza]] e [[knowledge/topics/installazione-e-deploy]].

## Confirmed decisions

### CI

| # | Regola |
|---|---|
| CI1 | **Workflow compatibili con GitHub Actions:** sintassi di GitHub Actions in `.gitstack/workflows/`, letti anche da `.github/workflows/`; action pronte (`uses:`) scaricate da GitHub o da un mirror interno scelto dall'amministratore. I workflow esistenti, compresi quelli di GitStack, girano quasi senza modifiche. Riuso di un esecutore open source esistente da valutare in sviluppo, con verifica della licenza rispetto ad AGPL. |
| CI2 | **Runner integrato più runner esterni:** di default ogni job è un pod usa e getta nel cluster, in uno spazio separato. Barriere fisse: niente privilegi, niente accesso alla rete interna del cluster (solo internet o proxy e l'API di GitStack con il token del job), build di immagini senza privilegi e senza accesso al runtime del nodo. Runner esterni registrati dall'amministratore con un token, scelti con `runs-on:`. |
| CI3 | **Eventi:** `push` (branch, tag, `paths`), `pull_request` (stesso repo), `workflow_dispatch` (con `write`), `schedule` al massimo ogni 15 minuti e sospeso dopo 60 giorni senza push, `[skip ci]`. Mai sui repo archiviati; spenta di default sui repo in mirror in pull. Eventi su issues e commenti dopo. |
| CI4 | **Segreti, variabili, token del job:** segreti e variabili di repo e di organizzazione (gestiti da `admin` del repo e owner), cifrati, mai riletti, mascherati nei log. Token del job solo per il repo e per la durata del job, riducibile con `permissions:`, **mai con più permessi di chi ha avviato il job**; i suoi push non avviano altri workflow. Ambienti con approvazione dopo la v2. |
| CI5 | **Log, artefatti, cache:** log in diretta per 90 giorni; artefatti 30 giorni, fino a 2 GB; cache fino a 10 GB per repo (meno usate e inutilizzate da 7 giorni eliminate). Valori regolabili; disco o S3; lettura con `read`; artefatti e cache fuori dai backup, log inclusi. |
| CI6 | **Risultati e agenti:** stati dei commit via PR9 (contesto `workflow / job`), usabili come obbligatori (PR4); scheda **Actions** con log in diretta, artefatti, «Riesegui», «Annulla», avvio a mano; notifica a chi ha avviato quando fallisce e quando torna verde; webhook `workflow_run`, `workflow_job`; `gs run …` e `gs workflow …` con `--json` e `--log-failed`; skills che insegnano ad aspettare la CI e correggere dai log. |
| CI7 | **Risorse:** job contemporanei sul runner integrato 1 (profilo minimo) o 3 (consigliato), modificabili; per job 2 vCPU, 4 GB, 20 GB di disco, 2 ore (i workflow possono solo chiedere meno); priorità sempre ai servizi di GitStack; coda equa (massimo metà dei posti per owner); un nuovo push annulla il run in coda dello stesso branch o PR; console con coda e risorse. |

### Registry

| # | Regola |
|---|---|
| RG1 | **Immagini legate al repo:** registry OCI standard; indirizzi `<host>/<owner>/<repo>[/<nome>]`; permessi e visibilità del repo (`read` scarica, `write` pubblica, `admin` elimina), nessun accesso anonimo; login con token personale o di agente (scope `registry`) o token del job; scheda **Packages**; archiviazione ed eliminazione seguono il repo (R2, R10). |
| RG2 | **Archiviazione e pulizia:** disco o S3; immagini senza tag eliminate dopo 7 giorni, quelle con tag mai da sole; regole facoltative per repo ("ultime N", "`sha-*` più vecchi di X giorni"); tag protetti facoltativi (es. `v*`); quote per owner e avviso oltre l'80% del disco. **Immagini escluse di default dai backup** (si ricreano dalla CI dai tag), includibili. Firme cosign e SBOM conservate come oggetti OCI; verifica al deploy con la v3. |

### Release

| # | Regola |
|---|---|
| REL1 | **Release come GitHub:** su un tag, da chi ha `write` (UI, API, `gs release`, CI); note Markdown con bozza generata da commit e PR; file allegati fino a 2 GB con SHA-256; bozza, pre-release, Latest; pagina **Releases** al posto di Tags (B7); webhook `release`; note nei backup, file allegati esclusi di default come le immagini. |

## Conseguenze da ricordare

- Dopo un ripristino senza immagini né file allegati, vanno ricostruiti dalla CI; per la v3, le app su k3s ripartono solo dopo la ricostruzione.
- Finché la v2-a non è pronta, la CI dei branch di GitStack dopo la tappa 2 richiede una soluzione ponte ([[knowledge/topics/rilascio-v1]]).

## Fuori dalla v2

Eventi su issues e commenti, ambienti con approvazione manuale, verifica obbligatoria delle firme al deploy (v3), fork.

## Related topics

- [[knowledge/topics/pull-request]]
- [[knowledge/topics/rilascio-v1]]
- [[knowledge/topics/visione]]

