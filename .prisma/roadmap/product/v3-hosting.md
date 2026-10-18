---
{"horizon":"later","id":"OBJ-ece89711-7a10-4f38-9aad-9106ddeeeeb1","knowledge":["DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e"],"reopen_reason":"Scope della v3 definito dalle regole H1–H13 e divisione in tre passi (operatore, 2026-10-18).","schema_version":1,"title":"v3 — Hosting di app e container","updated":"2026-10-18T16:59:03.707141400+00:00"}
---


# v3 — Hosting di app e container

## Outcome

Deploy di app e container su k3s da web UI, API, `gs` e MCP: app interne o pubbliche, domini e certificati automatici, variabili e segreti, volumi persistenti, log, metriche, rollback e scala a mano, tutto con i permessi del repo e utilizzabile da persone e agenti.

## Rationale

Completa la visione "piccolo Azure" sfruttando k3s già incluso (D2) e chiude ciò che la v2 rimandava: verifica delle firme al deploy e ripartenza delle app dopo un ripristino.

## Scope

Regole H1–H13 in [[knowledge/topics/hosting-app]] (analisi del 2026-10-18). Realizzazione dopo la v2, in tre passi in ordine, ciascuno utilizzabile da solo:

1. **v3-a App interne** (H2 indirizzo automatico, H4–H13);
2. **v3-b Dati** (H3);
3. **v3-c App pubbliche** (H1, H2 domini propri e Let's Encrypt).

Fuori dalla v3: database gestiti e multi-nodo («Oltre»), anteprime per ogni PR, definizione dell'app in un file del repo, scala automatica.

## Source references

- `.lmbrain-lite/knowledge/vision.md`
- Tema di analisi "v3 — Hosting di app e container: regole di prodotto" (H1–H13)

