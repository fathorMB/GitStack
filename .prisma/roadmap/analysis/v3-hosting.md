---
{"depends_on":["TOP-0b04024c-6f6b-4d35-b790-97d7591d6c08"],"id":"TOP-de29e79d-90cb-4163-a1fe-2978bed63f8d","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-b18abc32-db2c-4b14-8800-d37f5443dbad","DOC-84811cef-6f8e-4028-97d6-d47381172907"],"reopen_reason":"Regola su eliminazione del repo e app confermata dall'operatore il 2026-10-18.","schema_version":1,"state":"consolidated","title":"v3 — Hosting di app e container: regole di prodotto","updated":"2026-10-18T17:02:07.470805800+00:00"}
---


# v3 — Hosting di app e container: regole di prodotto

## Expected learning

Regole di prodotto per la v3 «Hosting» abbastanza precise da scomporla in sotto-tappe come la v2, analizzate in anticipo il 2026-10-18 a partire dalle otto domande del CEO; realizzazione «later», dopo v1.0, v1.1 e v2. Serviva anche a chiudere le regole della v2 che aspettavano la v3.

## Scelte confermate dall'operatore (2026-10-18)

Testo completo in [[knowledge/topics/hosting-app]].

- **H1** interne di default, pubbliche con doppio consenso (istanza + `admin` dell'app).
- **H2** indirizzo automatico sotto il dominio delle app più domini propri; CA interna o Let's Encrypt automatico.
- **H3** volumi persistenti inclusi nei backup; database gestiti esclusi.
- **H4** immagini in uso dalle app sempre nei backup (modifica RG2).
- **H5** l'app appartiene a un repo, con i permessi del repo; archiviazione del repo ferma le app; eliminazione del repo le ferma subito, le ripristina con il repo entro 7 giorni, poi le elimina con i volumi.
- **H6** login GitStack davanti alle app interne per chi ha `read` sul repo.
- **H7** deploy a mano e automatico da branch; anteprime PR dopo la v3.
- **H8** configurazione in GitStack con versioni; segreti come CI4 ma separati.
- **H9** barriere fisse, root permesso di default, «restricted» attivabile.
- **H10** la CI firma le immagini; deploy automatico solo con firme, a mano con etichetta «non verificata».
- **H11** parte garantita più tetto; priorità GitStack > app > CI; quote per owner.
- **H12** log, metriche, salute, rollback; scala solo a mano.
- **H13** agenti con la stessa API e i permessi del repo, deploy autonomo con `write`.
- **Ordine:** v3-a App interne, v3-b Dati, v3-c App pubbliche.

## Open questions

Nessuna.

## Consolidation summary

Consolidato il 2026-10-18 con conferma dell'operatore. La v3 si realizza in tre passi utilizzabili da soli: app interne senza disco, dati persistenti, app pubbliche con domini propri. Riallineate RG2 e CI7 in [[knowledge/topics/ci-registry-release]], i backup e il dominio delle app in [[knowledge/topics/installazione-e-deploy]], archiviazione ed eliminazione in [[knowledge/topics/repository-git]]. Idee rimandate nel Dream Journal: anteprime per PR, definizione dell'app in un file del repo, scala automatica.

