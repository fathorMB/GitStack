---
{"horizon":"later","id":"OBJ-a5afa7c2-2943-4650-92ed-8d0528fd0eda","knowledge":["DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-04fbc589-a144-403b-99e0-13e541fac16a"],"reopen_reason":"Terzo passo della v2 (operatore, 2026-10-12).","schema_version":1,"title":"v2-c — Release sui tag","updated":"2026-10-11T22:57:32.342576800+00:00"}
---


# v2-c — Release sui tag

## Outcome

Ogni versione ha una pagina di release con note e file allegati, creata a mano o dalla CI, e si scarica anche con `gs release`.

## Rationale

REL1 e B7: le release erano rimandate alla v2; permettono a GitStack di rilasciare se stesso da GitStack.

## Scope

Release su tag con note generate da commit e PR, file allegati fino a 2 GB con SHA-256, bozza, pre-release e Latest, pagina Releases al posto di Tags, `gs release`, webhook `release`. Regole in [[knowledge/topics/ci-registry-release]].

Terzo dei tre passi della v2, dopo v2-a e v2-b.

## Source references

- Tema di analisi "v2 — CI e registry container: regole di prodotto" (REL1)

