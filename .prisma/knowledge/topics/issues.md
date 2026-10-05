---
{"area":"requirements","id":"DOC-1565db4b-4108-490d-aca1-28d30c36ad99","related":["TOP-5dd4b056-b64a-4878-9266-6dc2e134dd00","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-6878277b-230e-448b-a761-215e9d233c54","OBJ-43ae9cfe-b093-4063-a4d8-90e6cf6827e2","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"Indicazioni per i coding agent sul mockup 13 aggiornato e sui confini di M-05, confermate dall'operatore il 2026-10-05.","schema_version":1,"sources":[],"tags":["issues","m-05","agenti","ricerca","mockup"],"title":"Issues: regole di prodotto","updated":"2026-10-05T19:57:33.120740800+00:00"}
---


# Issues: regole di prodotto

## Context

Regole per le issues (M-05) confermate dall'operatore il 2026-10-05 nel tema di analisi "Issues: regole di prodotto". Completano D10 ([[knowledge/topics/decisioni]]) e si appoggiano ai permessi `read` / `write` / `admin` di [[knowledge/topics/identita-e-sicurezza]] e alle regole dei repo di [[knowledge/topics/repository-git]] (chiusure sul branch principale, R4; issues di sola lettura nei repo archiviati, R10).

## Confirmed decisions

| # | Regola |
|---|---|
| I1 | **Numerazione condivisa con le Pull Request:** un solo contatore `#n` per repo, condiviso da issues e (v1.1) PR; i numeri non si riusano mai. |
| I2 | **Due stati, chiusura con motivo:** aperta / chiusa con motivo *completata*, *non pianificata* o *duplicata di #n*. `fixes #12` chiude come *completata*. Icone distinte e filtro `reason:`. Le issues non pianificate e duplicate non contano nell'avanzamento delle milestone. Riaprire azzera il motivo. Niente stati personalizzati. |
| I3 | **`read` apre e commenta, `write` gestisce:** chi vede il repo (anche per visibilità interna) apre issues e commenta; chi ha `write` assegna, mette etichette e milestone, chiude e riapre issues altrui. L'autore chiude e riapre sempre la propria. |
| I4 | **Modifiche tracciate, issues non eliminabili:** l'autore modifica il proprio testo con indicazione "edited"; chi ha `admin` vede le versioni precedenti. Commenti eliminabili da autore o `admin` con traccia "comment deleted". Le issues non si eliminano; chi ha `admin` può nasconderle (numero conservato, contenuto visibile solo a `admin`). |
| I5 | **Etichette predefinite:** alla creazione del repo (casella attiva di default) `bug`, `enhancement`, `documentation`, `question`, `duplicate`, `good first issue`, `agent-ready`, `needs-human`. Per repo, modificabili, senza automatismi nella v1. |
| I6 | **Fino a 10 assegnatari con `write`:** persone o agenti con `write`; ci si può autoassegnare (un agente "prende" il lavoro). Filtro `assignee:@agents`. |
| I7 | **Milestone per repo:** titolo, descrizione, data facoltativa; gestite da chi ha `write`; al massimo una milestone per issue. |
| I8 | **Menzioni rispettose della visibilità:** `@utente`, `@agente`, `@org/team` notificano solo chi vede il repo; altrimenti testo semplice, nessun accesso concesso. Suggerimenti solo di nomi validi. La menzione non assegna. |
| I9 | **Allegati protetti:** immagini, PDF, testo/log, ZIP fino a 10 MB (configurabile), sul volume dati, inclusi nel backup (D19), scaricabili solo da chi vede il repo; nessun URL pubblico. |
| I10 | **Ricerca con sintassi GitHub:** `is:`, `reason:`, `label:`, `assignee:` (`@me`, `@agents`), `author:`, `milestone:`, `no:`, `repo:`, `org:` e testo libero (ricerca testuale PostgreSQL). Nel repo e su tutta l'installazione, solo su repo visibili; stessa sintassi in UI, API e `gs`; la Home la usa. |
| I11 | **Blocco e modelli sì, trasferimento no:** chi ha `admin` blocca la discussione (poi commenta solo `write`); modelli Markdown versionati in `.gitstack/ISSUE_TEMPLATE/`, proposti da UI, API e `gs`. Nessun trasferimento tra repo. |

## Mockup e confini di M-05

Confermato dall'operatore il 2026-10-05, per chi realizza le UI mancanti di M-05:

- **Schermata 13 "Issue detail"** di `design/mockups-v1/index.html#issue` ha una barra scura di varianti (non fa parte del prodotto). Ogni variante è un caso da realizzare: *Normale* (con traccia "comment deleted", I4); *Chiusura con motivo* (pulsante diviso "Close as completed" con menu completata / non pianificata / duplicata di #n, I2); *Bloccata vista da read* (avviso al posto dell'editor, niente comandi di gestione, I11 e I3); *Nascosta vista admin* (avviso, anteprima di cosa vedono gli altri, "Unhide issue", I4); *Repo archiviato* (etichetta Archived, avviso, nessun editor né chiusura/riapertura, R10).
- **Chi ha solo `read`** non vede le rotelline di assegnatari, etichette e milestone né la sezione Admin (I3).
- **Fuori da M-05:** gli elementi con l'etichetta tratteggiata **M-06** (riferimento da altro repo, commit collegati, chiusura da commit, Unsubscribe/notifiche) e **M-07** (comando `gs`) arrivano con quelle milestone. In M-05 la chiusura è solo manuale con motivo.
- **Schermata 20 "New issue"** invariata.

## Fuori dalla v1

Stati personalizzati e Kanban (I2, D10), etichette e milestone di organizzazione (I5, I7), trasferimento di issues (I11), comportamenti automatici legati alle etichette (I5).

## Related topics

- [[knowledge/topics/decisioni]]
- [[knowledge/topics/identita-e-sicurezza]]
- [[knowledge/topics/repository-git]]
- [[knowledge/topics/stato-di-realizzazione]]

