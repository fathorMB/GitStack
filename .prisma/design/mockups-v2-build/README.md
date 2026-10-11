# Mockups · v2 Build (CI, registry, release)

Schermate della v2 (regole CI1–CI7, RG1–RG2, REL1 in `knowledge/topics/ci-registry-release.md`). Stesso stile e stessa barra di navigazione di `mockups-v1`: la barra scura in alto e le barre delle varianti non fanno parte del prodotto.

| # | Schermata | Passo | Regole |
|---|-----------|-------|--------|
| B1 | Actions: esecuzioni, filtri, coda; variante avvio a mano | v2-a | CI1, CI3, CI6, CI7 |
| B2 | Dettaglio esecuzione (varianti: fallita, in corso): job, passi, log mascherati, artefatti, stati dei commit, riesegui | v2-a | CI2, CI4, CI5, CI6 |
| B3 | Settings · Secrets &amp; variables e permessi del token del job | v2-a | CI4 |
| B4 | Admin · CI &amp; runners: posti, limiti, coda, runner esterni, spazio occupato | v2-a | CI2, CI5, CI7 |
| B5 | Packages: immagini del repo, tag protetti, firme e SBOM, comando di pull | v2-b | RG1, RG2 |
| B6 | Settings · Packages: pulizia, tag protetti, inclusione nei backup | v2-b | RG2 |
| B7 | Releases: Latest, pre-release, bozza, note generate, file allegati con SHA-256 | v2-c | REL1 |
| B8 | Terminale: `gs run watch`, `--log-failed`, `gs workflow run`, `gs release` | v2-a, v2-c | CI6, REL1 |

La pagina Releases sostituisce la schermata 23 (Tags) dei mockup v1 quando arriva la v2-c.

Dati finti: organizzazione "Acme", repo `acme/GitStack`, istanza `homehub.local`, agente `build-agent`.
