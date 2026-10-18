---
{"horizon":"later","id":"OBJ-a9f00bcc-bbe6-4a91-bc24-963bae228761","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-d1718775-770d-4065-8b27-3dc7a400dad6"],"reopen_reason":"Primo passo della v3 (operatore, 2026-10-18).","schema_version":1,"title":"v3-a — App interne","updated":"2026-10-18T16:59:03.708309500+00:00"}
---


# v3-a — App interne

## Outcome

Chi ha `write` su un repo mette in esecuzione un'app senza disco, raggiungibile in rete aziendale con il login GitStack, la aggiorna a mano o a ogni push sul branch scelto, ne legge log e metriche e torna indietro con un clic; un agente fa lo stesso con `gs app` o MCP.

## Rationale

H5–H13: è il nucleo della v3 e il passo più sicuro da rilasciare per primo, senza esposizione su internet mentre si collaudano isolamento e risorse.

## Scope

Scheda **Apps** nel repo (H5); indirizzo automatico sotto il dominio delle app con CA interna (parte di H2); login GitStack davanti all'app (H6); deploy a mano e da branch (H7); configurazione con versioni e segreti (H8); isolamento (H9); firme al deploy e firma automatica dalla CI (H10); parte garantita, tetto e quote (H11); log, metriche, salute, rollback e scala a mano (H12); agenti, MCP e skills (H13); immagini in uso nei backup (H4). Solo app senza volumi.

Primo dei tre passi della v3; dipende da v2-a (CI) e v2-b (registry).

## Source references

- Tema di analisi "v3 — Hosting di app e container: regole di prodotto" (H2, H4–H13)

