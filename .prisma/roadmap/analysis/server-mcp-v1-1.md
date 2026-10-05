---
{"depends_on":["TOP-5cc14fc3-52fa-4baa-ae1b-6cd27bf145e4","TOP-4538888e-9b10-4aed-937a-f1d3bd849a67"],"id":"TOP-fdc4dac8-adea-4a7c-9cd2-ae4dece969c2","knowledge":["DOC-b18abc32-db2c-4b14-8800-d37f5443dbad","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-84811cef-6f8e-4028-97d6-d47381172907","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358"],"reopen_reason":"Consolidamento proposto all'operatore.","schema_version":1,"state":"consolidated","title":"v1.1 — Server MCP: regole di prodotto","updated":"2026-10-05T20:44:55.121133400+00:00"}
---


# v1.1 — Server MCP: regole di prodotto

## Expected learning

Regole di prodotto per il server MCP della v1.1 (obiettivo "v1.1 — Collaborazione"): dove gira, cosa espone, come si autentica, quali protezioni ha.

## Scelte confermate dall'operatore (2026-10-05)

M1–M5, testo completo in [[knowledge/topics/server-mcp]]: `gs mcp` locale dentro la CLI; strumenti scelti generati dal contratto OpenAPI più `api_request`; token di `gs`, modalità sola lettura, annotazione distruttiva e operazioni pericolose senza strumento dedicato; flussi delle skills come prompt MCP, niente risorse; nessuna installazione separata, configurazione pronta in `/downloads` e avviso di versione.

## Open questions

Nessuna.

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole M1–M5 riportate in [[knowledge/topics/server-mcp]] e nell'obiettivo v1.1. Con le Pull Request (PR1–PR12) la v1.1 è interamente analizzata. Resta da aggiornare il mockup 24 (CLI & skills) con la configurazione MCP pronta da copiare (M5).

