---
{"horizon":"now","id":"OBJ-397d65b0-f4a8-4a4c-b965-08d5e41c8be0","knowledge":["DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-04fbc589-a144-403b-99e0-13e541fac16a","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e"],"reopen_reason":"Orizzonte now dal 2026-10-26: passi realizzati in parallelo, non più dopo v1.0 e v1.1.","schema_version":1,"title":"v2 — Build: CI e registry container","updated":"2026-10-25T23:32:19.363317300+00:00"}
---



# v2 — Build: CI e registry container

## Outcome

CI in stile GitHub Actions che reagisce a push, PR e avvii manuali, registry di immagini container e release sui tag, tutto dentro GitStack e utilizzabile da persone e agenti.

## Rationale

Primo passo verso il "piccolo cloud privato"; si aggancia agli eventi e alle risorse generiche predisposti in v1 (D15) e agli stati dei commit della v1.1 (PR9). Per GitStack stesso restituisce la CI ai branch di lavoro dopo il passaggio della casa su homehub.

## Scope

Regole in [[knowledge/topics/ci-registry-release]] (analisi del 2026-10-12). **Dal 2026-10-26** (scelta dell'operatore) i tre passi non aspettano più v1.0 e v1.1: si chiudono in parallelo, ciascuno utilizzabile da solo, e la v2 esce appena è completa:

1. **v2-a CI** (CI1–CI7);
2. **v2-b Registry** (RG1–RG2);
3. **v2-c Release** (REL1, al posto della pagina Tags di B7).

Fuori dalla v2: eventi su issues e commenti, ambienti con approvazione manuale, verifica obbligatoria delle firme al deploy (v3).

## Source references

- `.lmbrain-lite/knowledge/vision.md`
- Tema di analisi "v2 — CI e registry container: regole di prodotto"

