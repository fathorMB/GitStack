---
{"area":"requirements","id":"DOC-3595cb24-e019-4230-a412-009769bae527","related":["TOP-bfd1d6ab-4456-41c0-b08c-3881bfb18690","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-84811cef-6f8e-4028-97d6-d47381172907"],"reopen_reason":"Nuova regola MP8: import una tantum (\"Copia una volta sola\") nella v1, scelta dell'operatore del 2026-10-12.","schema_version":1,"sources":[],"tags":["mirror","git","dogfooding","m-09","import"],"title":"Mirror in pull: regole di prodotto","updated":"2026-10-11T23:06:41.377265400+00:00"}
---


# Mirror in pull: regole di prodotto

## Context

Regole confermate dall'operatore il 2026-10-12 nel tema di analisi "Mirror in pull" (durante l'analisi chiamate M1–M7, rinominate **MP1–MP7** perché M1–M5 sono le regole del server MCP; MP8 aggiunta lo stesso giorno). GitStack scarica periodicamente un repo Git esterno (per esempio da GitHub) e ne tiene una copia. Primo uso: la copia di backup del repo GitHub di GitStack su homehub (tappa 1 di V7, [[knowledge/topics/rilascio-v1]]); a tendere, i clienti che migrano da GitHub. Fa parte di M-09 come primo pezzo. È il gemello del mirror in push (V8-mirror) e ne riusa comportamento e protezioni.

## Confirmed decisions

| # | Regola |
|---|---|
| MP1 | **Copia prudente:** tutti i branch e tutti i tag; non si cancella e non si sovrascrive mai niente. Un ref riscritto (force-push) o cancellato nell'origine resta su GitStack nella versione precedente, è segnato `diverged` e gli admin del repo sono avvisati; gli altri ref continuano ad aggiornarsi. |
| MP2 | **Codice in sola lettura, issues normali:** finché il mirror è attivo ogni push (HTTPS, SSH, `gs`) è rifiutato con un messaggio che indica l'origine; solo il mirror aggiorna i ref. Etichetta **Mirror** con origine e ora dell'ultimo aggiornamento. Issues, etichette, milestone e commenti funzionano come in ogni repo. |
| MP3 | **Creazione e credenziali:** su un repo nuovo ("Importa da URL" con "mantieni sincronizzato") o esistente ("Attiva mirror in pull"), da chi ha `admin` sul repo o dall'amministratore dell'installazione. Su un repo con contenuti vale MP1. Credenziali facoltative (utente e token), cifrate, mai restituite dall'API, mai nei log né nella riga di comando di git. |
| MP4 | **Frequenza ed errori:** ogni 10 minuti di default, regolabile da `admin` tra 5 minuti e 24 ore; «Sincronizza ora». Tentativi con attesa crescente (30 s, 2 min, 10 min, 1 h, 3 h, 6 h); un blocco della policy di uscita (C8) non si ritenta. Notifica agli admin del repo se non c'è un aggiornamento riuscito da più di 24 ore. Un aggiornamento alla volta, con timeout; registro delle ultime esecuzioni (50, 30 giorni). |
| MP5 | **Origini ammesse:** solo `https://`, senza credenziali nell'indirizzo, controllate con `pkg/egress` (loopback, cluster, link-local e intervalli speciali sempre bloccati; liste ammesse e vietate dell'amministratore). SSH e `git://` dopo la v1. |
| MP6 | **Pausa e interruzione:** **Pausa** ferma gli aggiornamenti lasciando il repo mirror in sola lettura; **Interrompi** (con il nome del repo da riscrivere, come G8) rende il repo normale e scrivibile, cancella le credenziali, conserva registro e ref `diverged`; si può riattivare con MP3. Con il mirror in pull attivo non si può configurare un mirror in push verso lo stesso indirizzo dell'origine. |
| MP7 | **Interfaccia, API, `gs`, confini:** UI ("Importa da URL", sezione **Mirror in pull** nelle impostazioni con origine, intervallo, «Sincronizza ora», Pausa, Interrompi, ref `diverged`, registro; etichetta **Mirror**); API per tutto ciò che fa la UI (D7). Nessun comando `gs` nuovo: `gs repo view` mostra mirror e origine, il resto via `gs api` (G4). Si copia solo il contenuto Git: niente issues, PR, release, wiki, etichette dell'origine, niente LFS (R8). |
| MP8 | **Import una tantum ("Copia una volta sola"):** nello stesso modulo "Importa da URL", alternativa a "mantieni sincronizzato". Stesse origini e protezioni (MP5) e stesse credenziali facoltative (MP3); si copiano una volta tutti i branch e i tag, poi le credenziali sono cancellate e il repo è normale e scrivibile, senza legami con l'origine. Equivale a "crea mirror, sincronizza, interrompi" (MP6) in un passo. Disponibile anche via API (D7). Percorso di migrazione da GitHub. |

Le regole MP1–MP8 formano una famiglia nella tabella regole→test (`docs/rules-coverage.md`) e rientrano nel criterio V1.

## Fuori dalla v1

Origini SSH e `git://`, Git LFS, importazione di issues, PR e release dall'origine (idea nel Dream Journal), aggiornamento immediato via webhook dell'origine.

## Related topics

- [[knowledge/topics/rilascio-v1]]
- [[knowledge/topics/repository-git]]
- [[knowledge/topics/collegamenti-notifiche-webhook]]
- [[knowledge/topics/cli-gs-skills]]

