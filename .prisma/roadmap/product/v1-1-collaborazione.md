---
{"horizon":"later","id":"OBJ-33ee21a1-e5f1-4a4e-8668-4dfb162d8446","knowledge":["DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","DOC-b18abc32-db2c-4b14-8800-d37f5443dbad"],"reopen_reason":"Scope del server MCP definito dalle regole M1–M5 (2026-10-05); orizzonte invariato.","schema_version":1,"title":"v1.1 — Collaborazione: Pull Request e server MCP","updated":"2026-10-05T20:44:55.192580700+00:00"}
---


# v1.1 — Collaborazione: Pull Request e server MCP

## Outcome

Pull Request con diff, review e merge; server MCP generato dalla stessa API pubblica.

## Rationale

D8 e D9: le PR sono il flusso chiave di collaborazione con gli agenti; il modello dati le prevede già dalla v1. Il server MCP porta GitStack negli assistenti AI che non usano il terminale.

## Scope

**Pull Request** — regole in [[knowledge/topics/pull-request]] (PR1–PR12): PR tra branch dello stesso repo; review con commenti sulle righe e conversazioni risolte; approvazione valida solo da persone con `write`; merge commit o squash; impostazione "Richiedi una Pull Request" sul branch principale con approvazioni minime e stati obbligatori; bozze; conflitti mostrati e risolti in locale; chiusura delle issues al merge; API degli stati dei commit per CI esterne; comandi `gs pr` e flusso degli agenti nelle skills; notifiche ed eventi webhook delle PR; filtri di ricerca per le PR e casella Viewed. Mockup 25–28.

**Server MCP** — regole in [[knowledge/topics/server-mcp]] (M1–M5): comando locale `gs mcp` con login e istanze di `gs`; circa 30–40 strumenti scelti generati dal contratto OpenAPI più `api_request`; modalità sola lettura, annotazione distruttiva e operazioni pericolose senza strumento dedicato; flussi delle skills come prompt MCP; configurazione pronta in `/downloads` e avviso di versione.

Fuori dalla v1.1: fork, rebase, suggerimenti applicabili e risoluzione dei conflitti dal browser; endpoint MCP remoto con OAuth e risorse MCP.

## Source references

- `.lmbrain-lite/knowledge/vision.md`
- Tema di analisi "v1.1 — Pull Request: regole di prodotto" (PR1–PR12)
- Tema di analisi "v1.1 — Server MCP: regole di prodotto" (M1–M5)

