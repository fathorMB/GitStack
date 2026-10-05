---
{"area":"requirements","id":"DOC-84811cef-6f8e-4028-97d6-d47381172907","related":["TOP-5cc14fc3-52fa-4baa-ae1b-6cd27bf145e4","DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","OBJ-0820d6e7-7b4f-4c7c-b84d-a3ef18d5bc81"],"schema_version":1,"sources":[],"tags":["cli","gs","skills","agenti","m-07"],"title":"CLI gs e skills per agenti: regole di prodotto","updated":"2026-10-05T09:50:00+00:00"}
---

# CLI gs e skills per agenti: regole di prodotto

## Context

Regole per M-07 confermate dall'operatore il 2026-10-05 nel tema di analisi "CLI gs e skills per agenti: regole di prodotto". Completano D8 e D17 ([[knowledge/topics/decisioni]]) e il contratto di [[knowledge/topics/contratto-api]]. `gs` espone ciò che le altre regole promettono "anche via `gs`": notifiche (C4), ricerca (I10), blame e ricerca nel codice (B4, B5).

## Confirmed decisions

| # | Regola |
|---|---|
| G1 | **Familiare come `gh`, non un clone:** stessi comandi e flag dove i concetti coincidono; comandi e opzioni aggiuntivi nello stesso stile per le regole proprie di GitStack; nessuna compatibilità piena promessa. |
| G2 | **Token:** `gs auth login` (istanza + token; `--web` apre la pagina "nuovo token" con scope consigliati); `GS_TOKEN` e `GS_HOST` per agenti e CI; token nel portachiavi del sistema o in file privato; `gs auth setup-git` per HTTPS; `gs auth status`. Nessun login automatico dal browser nella v1. |
| G3 | **Output:** testo di default; `--json` con campi selezionabili e `--jq`. Campi JSON = contratto (si aggiungono, non si tolgono né rinominano senza nuova versione maggiore). Codici di uscita: `0` ok, `1` errore, `2` uso errato, `4` non autenticato, `5` permesso negato, `6` non trovato; errori JSON su stderr. |
| G4 | **Comandi v1:** `auth`, `repo` (create, list, view, clone, edit, archive, delete, restore), `issue` (create, list, view, edit, comment, close, reopen, lock), `label`, `milestone`, `search`, `notification`, `browse`, `blame`, `skills`; amministrazione tramite `gs api`. |
| G5 | **Skills Agent Skills:** cartelle con `SKILL.md`; `gs skills install` (rileva l'agente, progetto o utente), `gs skills update`, `--agents-md` per `AGENTS.md`. Pacchetto v1: installazione e login, issue fino al commit, triage, gestione repo, ciclo notifiche. Apache-2.0. |
| G6 | **Distribuzione:** binari `gs` (Linux, macOS, Windows; amd64, arm64) e skills serviti da ogni istanza su `/downloads`, script `install-gs.sh` e PowerShell, funzionante air-gapped; copia nelle release pubbliche. Controllo di versione con il server: avviso, rifiuto solo su versione maggiore diversa. |
| G7 | **Più istanze:** istanza e `owner/repo` dal remote `origin` (HTTPS o SSH anche su porta 2222); fuori dai repo `GS_HOST` o istanza predefinita; `--hostname`, `--repo`. |
| G8 | **Operazioni distruttive:** conferma interattiva (nome del repo per eliminare o archiviare); senza terminale fallisce con codice `2` salvo `--yes`; le skills vietano `--yes` agli agenti senza istruzione esplicita; gli scope del token restano il limite. |

## Fuori dalla v1

Login automatico dal browser (G2), comandi admin dedicati (G4), gestori di pacchetti Homebrew/apt/winget (G6). Server MCP in v1.1 (D8).

## Related topics

- [[knowledge/topics/contratto-api]]
- [[knowledge/topics/identita-e-sicurezza]]
- [[knowledge/topics/collegamenti-notifiche-webhook]]
- [[knowledge/topics/installazione-e-deploy]]
