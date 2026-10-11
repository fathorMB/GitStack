---
{"depends_on":[],"id":"TOP-bfd1d6ab-4456-41c0-b08c-3881bfb18690","knowledge":["DOC-3595cb24-e019-4230-a412-009769bae527","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-84811cef-6f8e-4028-97d6-d47381172907"],"reopen_reason":"Tutte le domande del tema risolte e confermate dall'operatore (2026-10-12).","schema_version":1,"state":"consolidated","title":"Mirror in pull: copia di un repo esterno dentro GitStack","updated":"2026-10-11T22:40:26.375016600+00:00"}
---


# Mirror in pull: copia di un repo esterno dentro GitStack

## Expected learning

Regole di prodotto per una funzione nuova: GitStack scarica periodicamente un repo Git esterno (per esempio da GitHub) e ne tiene una copia. Serve subito per la copia di backup del repo GitHub di GitStack su homehub (tappa 1 di V7) e, a tendere, ai clienti che migrano da GitHub. Scelta dell'operatore del 2026-10-12: funzione del prodotto, non script esterno, da fare subito.

## Scelte confermate dall'operatore (2026-10-12)

Durante l'analisi le regole erano chiamate M1–M7; sono state rinominate **MP1–MP7** perché M1–M5 sono le regole del server MCP. Testo completo in [[knowledge/topics/mirror-in-pull]].

- **MP1** copia prudente: tutti i branch e i tag, mai cancellare o sovrascrivere, ref riscritti segnati `diverged` con avviso.
- **MP2** codice in sola lettura finché il mirror è attivo, issues normali, etichetta Mirror.
- **MP3** creazione su repo nuovo o esistente da chi ha `admin`; credenziali facoltative cifrate.
- **MP4** ogni 10 minuti (5 min–24 h), «Sincronizza ora», tentativi come il mirror in push, notifica dopo 24 ore senza aggiornamenti.
- **MP5** solo `https://` con `pkg/egress`; SSH dopo la v1.
- **MP6** Pausa e Interrompi con conferma; niente mirror in push verso la stessa origine.
- **MP7** UI e API complete, nessun comando `gs` nuovo, solo contenuto Git.
- **Collocazione:** dentro M-09 come primo pezzo, prima del resto di M-09 e in parallelo al resto di M-08.

## Open questions

Nessuna.

## Consolidation summary

Consolidato il 2026-10-12 con conferma dell'operatore. Il mirror in pull è il primo pezzo di M-09 e realizza la tappa 1 del passaggio su homehub (copia di backup del repo GitHub); la tappa 2 (casa su GitStack, prima della v1.0) si fa con Interrompi e il mirror in push esistente. Regole MP1–MP7 in [[knowledge/topics/mirror-in-pull]]; V1 e V7 aggiornate in [[knowledge/topics/rilascio-v1]]; idea dell'importazione delle issues da GitHub nel Dream Journal.

