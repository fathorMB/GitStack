---
{"horizon":"now","id":"OBJ-a9f00bcc-bbe6-4a91-bc24-963bae228761","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-d1718775-770d-4065-8b27-3dc7a400dad6"],"reopen_reason":"Orizzonte now dal 2026-10-26; aggiunte H14–H16, lista di accesso (H6) e deploy su regola di tag (H7).","schema_version":1,"title":"v3-a — App interne","updated":"2026-10-25T23:32:19.372972800+00:00"}
---



# v3-a — App interne

## Outcome

Chi ha `write` su un repo mette in esecuzione un'app senza disco, raggiungibile in rete aziendale con il login GitStack, la aggiorna a mano, a ogni push sul branch scelto o a ogni tag che rispetta una regola, ne legge log e metriche e torna indietro con un clic; un agente fa lo stesso con `gs app` o MCP.

## Rationale

H5–H16: è il nucleo della v3 e il passo più sicuro da rilasciare per primo, senza esposizione su internet mentre si collaudano isolamento e risorse. Orizzonte now dal 2026-10-26; la prima parte è il [[roadmap/product/pilota-retail-it]].

## Scope

Scheda **Apps** nel repo (H5); indirizzo automatico sotto il dominio delle app con CA interna (parte di H2); login GitStack davanti all'app con lista di accesso per i tester esterni (H6); deploy a mano, da branch o da regola sui tag (H7); configurazione con versioni e segreti (H8); isolamento (H9); firme al deploy e firma automatica dalla CI (H10); parte garantita, tetto e quote (H11); log, metriche, salute, rollback e scala a mano (H12); agenti, MCP e skills (H13); variabili di sistema `GITSTACK_` (H14); nomi interni tra app dello stesso repo (H15); uscita verso internet e rete privata (H16); immagini in uso nei backup (H4). Solo app senza volumi.

Primo dei tre passi della v3; dipende da v2-a (CI) e v2-b (registry).

## Source references

- Tema di analisi "v3 — Hosting di app e container: regole di prodotto" (H2, H4–H13)
- Richiesta retail.it del 2026-10-26 (H6, H7, H14–H16)

