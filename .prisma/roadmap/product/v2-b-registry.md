---
{"horizon":"later","id":"OBJ-58a0b0bf-3830-4ce4-9ae3-0c7504240bde","knowledge":["DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-d1718775-770d-4065-8b27-3dc7a400dad6"],"reopen_reason":"Secondo passo della v2 (operatore, 2026-10-12).","schema_version":1,"title":"v2-b — Registry di immagini container","updated":"2026-10-11T22:57:32.341611900+00:00"}
---


# v2-b — Registry di immagini container

## Outcome

Ogni repo può pubblicare immagini OCI su GitStack, con i suoi permessi e la sua visibilità, da `docker`, dalla CI o dagli agenti.

## Rationale

RG1–RG2: chiude il ciclo build della CI e prepara la v3, dove le app su k3s useranno le immagini del proprio repo.

## Scope

Registry OCI legato ai repo, login con token, scheda Packages, pulizia automatica, tag protetti, quote, disco o S3, immagini escluse di default dai backup, firme cosign e SBOM conservate. Regole in [[knowledge/topics/ci-registry-release]].

Secondo dei tre passi della v2, dopo v2-a.

## Source references

- Tema di analisi "v2 — CI e registry container: regole di prodotto" (RG1–RG2)

