---
{"area":"requirements","id":"DOC-b18abc32-db2c-4b14-8800-d37f5443dbad","related":["TOP-fdc4dac8-adea-4a7c-9cd2-ae4dece969c2","DOC-84811cef-6f8e-4028-97d6-d47381172907","DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","OBJ-33ee21a1-e5f1-4a4e-8668-4dfb162d8446"],"reopen_reason":"M5 allineata a G6 (rifiuto solo su versione maggiore diversa), confermato dall'operatore il 2026-10-05.","schema_version":1,"sources":[],"tags":["mcp","v1.1","agenti","gs"],"title":"Server MCP (v1.1): regole di prodotto","updated":"2026-10-05T20:47:13.232069600+00:00"}
---


# Server MCP (v1.1): regole di prodotto

## Context

Regole per il server MCP della v1.1 confermate dall'operatore il 2026-10-05 nel tema di analisi "v1.1 — Server MCP: regole di prodotto". Completano D8 ([[knowledge/topics/decisioni]]: MCP generato dalla stessa API), le regole di `gs` G1–G8 ([[knowledge/topics/cli-gs-skills]]), il contratto API ([[knowledge/topics/contratto-api]]) e le regole degli agenti P4/P5 ([[knowledge/topics/identita-e-sicurezza]]) e PR10 ([[knowledge/topics/pull-request]]). Mockup: riquadro MCP della schermata 24.

## Confirmed decisions

| # | Regola |
|---|---|
| M1 | **Locale, dentro la CLI:** il server MCP è `gs mcp`, avviato dall'assistente sul proprio computer (stdio), con login, token, istanze e configurazione di `gs`. Nessun servizio nuovo sul server. Endpoint remoto `/mcp` fuori dalla v1.1. |
| M2 | **Strumenti scelti, generati dall'API:** le operazioni segnate "esponi in MCP" nel contratto OpenAPI diventano strumenti (circa 30–40: repo, codice, issues, PR e review, etichette, milestone, ricerca, notifiche, stati dei commit), con nomi `oggetto_azione` coerenti con `gs`. Strumento generico `api_request` per il resto, entro i permessi del token. |
| M3 | **Protezioni:** token di `gs`, niente OAuth; `--read-only` (solo letture, anche per `api_request`); annotazione MCP **distruttiva** su chiusure, merge, eliminazioni e occultamenti (definita nel contratto); nessuno strumento dedicato per eliminare/archiviare repo, impostazioni, webhook, token e amministrazione (solo `api_request`). Nessuna conferma propria del server. Account agent con permessi minimi consigliato dalle skills. |
| M4 | **Strumenti e prompt, niente risorse:** i flussi delle skills (issue fino alla PR, triage, review di PR) sono anche prompt MCP, generati dallo stesso pacchetto di skills. Nessuna risorsa MCP nella v1.1. |
| M5 | **Distribuzione e versioni:** `gs mcp` è dentro `gs`; `/downloads` mostra la configurazione pronta per gli assistenti più diffusi; `--host` per scegliere l'istanza. Versioni come G6: se `gs` è più vecchio dell'istanza nella stessa versione maggiore, il server parte con gli strumenti che conosce e avvisa di aggiornare; con una **versione maggiore diversa** non parte e spiega di aggiornare da `/downloads` (corretto il 2026-10-05 per allinearsi a G6). |

## Fuori dalla v1.1

Endpoint MCP remoto nell'istanza e login OAuth per client MCP (M1, M3), risorse MCP (M4), conferme gestite dal server (M3).

## Related topics

- [[knowledge/topics/cli-gs-skills]]
- [[knowledge/topics/contratto-api]]
- [[knowledge/topics/pull-request]]
- [[knowledge/topics/identita-e-sicurezza]]

