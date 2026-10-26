# Mockups · v2 Build (CI, registry, release)

Schermate della v2 (regole CI1–CI7, RG1–RG2, REL1 in `knowledge/topics/ci-registry-release.md`). Stesso stile e stessa barra di navigazione di `mockups-v1`: la barra scura in alto e le barre delle varianti non fanno parte del prodotto.

| # | Schermata | Passo | Regole |
|---|-----------|-------|--------|
| B1 | Actions: esecuzioni, filtri, coda, run da sincronizzazione del mirror; variante avvio a mano | v2-a | CI1, CI3, CI6, CI7 |
| B2 | Dettaglio esecuzione (varianti: fallita, in corso): job, passi, log mascherati, artefatti, stati dei commit, riesegui | v2-a | CI2, CI4, CI5, CI6 |
| B3 | Settings · Secrets &amp; variables, permessi del token del job, CI sul mirror | v2-a | CI3, CI4, RG2 |
| B4 | Admin · CI &amp; runners: posti, limiti, build delle immagini, coda, runner esterni, spazio occupato | v2-a | CI2, CI5, CI7 |
| B5 | Packages: immagini del repo, tag protetti, firme e SBOM, comando di pull | v2-b | RG1, RG2 |
| B6 | Settings · Packages: pulizia, tag protetti, inclusione nei backup | v2-b | RG2 |
| B7 | Releases: Latest, pre-release, bozza, note generate, file allegati con SHA-256 | v2-c | REL1 |
| B8 | Terminale: `gs run watch`, `--log-failed`, `gs workflow run`, deploy di un tag da workflow, `gs release` | v2-a, v2-c | CI6, REL1, H7 |

Aggiornamento 2026-10-18 (regole v3 H4, H10): B4, B5 e B6 mostrano che le immagini in uso dalle app entrano sempre nei backup e che la CI firma le immagini che pubblica (quelle caricate a mano risultano «non verificate»).

Aggiornamento 2026-10-26 (CI2, CI3, H7, RG2): B1 mostra un run partito dalla sincronizzazione del mirror a nome di chi ha acceso la CI; B3 ha l'interruttore «CI on this mirror» e non ha più il segreto `COSIGN_KEY` (la CI firma con la chiave dell'istanza); B4 indica la build con BuildKit senza privilegi come strada standard; B8 mostra un workflow che fa il deploy di un tag preciso su un'app v3 con il token del job.

La pagina Releases sostituisce la schermata 23 (Tags) dei mockup v1 quando arriva la v2-c.

Dati finti: organizzazione "Acme", repo `acme/GitStack` (in mirror da GitHub), istanza `homehub.local`, agente `build-agent`.
