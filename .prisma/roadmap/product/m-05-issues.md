---
{"horizon":"next","id":"OBJ-43ae9cfe-b093-4063-a4d8-90e6cf6827e2","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-1565db4b-4108-490d-aca1-28d30c36ad99"],"reopen_reason":"Regole I1–I11 sulle issues (tema consolidato il 2026-10-05).","schema_version":1,"title":"Issues","updated":"2026-10-05T08:42:00+00:00"}
---

# Issues

## Outcome

Ogni repo ha le sue issues con commenti, assegnatari, etichette, milestone e ricerca, usabili da UI e API.

## Rationale

D10: le issues legano il lavoro al codice e servono agli agenti per filtrare e chiudere lavoro.

## Scope

Issues numerate per repo, apertura/chiusura, commenti Markdown con cronologia, assegnatari, etichette colorate, milestone con avanzamento, ricerca e filtri (stato, etichetta, assegnatario, milestone, testo), UI di lista, dettaglio ed editor. Bacheca Kanban esclusa dalla v1.

Regole in [[knowledge/topics/issues]] (I1–I11): numerazione condivisa con le PR; chiusura con motivo; `read` apre e commenta, `write` gestisce; modifiche tracciate, issues nascondibili ma non eliminabili; etichette predefinite con `agent-ready` e `needs-human`; fino a 10 assegnatari con `write`; milestone per repo; menzioni di utenti e team nel rispetto della visibilità; allegati fino a 10 MB protetti dai permessi; ricerca con sintassi GitHub su repo e installazione; blocco della discussione; modelli di issue in `.gitstack/ISSUE_TEMPLATE/`.

Fuori dalla v1: stati personalizzati, etichette e milestone di organizzazione, trasferimento di issues tra repo.

Priorità 5 di 9 nella v1. Mockup 12, 13 e una nuova schermata "New issue".

## Source references

- `.lmbrain-lite/milestones/M-05.md`
- Tema di analisi "Issues: regole di prodotto" (I1–I11)
