---
{"horizon":"now","id":"OBJ-43ae9cfe-b093-4063-a4d8-90e6cf6827e2","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"M-05 è il lavoro in corso (GIT-101…GIT-109 su main): passa da next a now.","schema_version":1,"title":"Issues","updated":"2026-10-05T19:53:06.227459100+00:00"}
---


# Issues

## Outcome

Ogni repo ha le sue issues con commenti, assegnatari, etichette, milestone e ricerca, usabili da UI e API.

## Rationale

D10: le issues legano il lavoro al codice e servono agli agenti per filtrare e chiudere lavoro.

## Scope

Issues numerate per repo, apertura/chiusura, commenti Markdown con cronologia, assegnatari, etichette colorate, milestone con avanzamento, ricerca e filtri (stato, etichetta, assegnatario, milestone, testo), UI di lista, dettaglio ed editor. Bacheca Kanban esclusa dalla v1.

Regole in [[knowledge/topics/issues]] (I1–I11): numerazione condivisa con le PR; chiusura con motivo; `read` apre e commenta, `write` gestisce; modifiche tracciate, issues nascondibili ma non eliminabili; etichette predefinite con `agent-ready` e `needs-human`; fino a 10 assegnatari con `write`; milestone per repo; menzioni di utenti e team nel rispetto della visibilità; allegati fino a 10 MB protetti dai permessi; ricerca con sintassi GitHub su repo e installazione; blocco della discussione; modelli di issue in `.gitstack/ISSUE_TEMPLATE/`.

Avanzamento al commit `1587133` (2026-10-05):
- realizzato: contratto API e schema, sintassi di ricerca, issues in core con numerazione, stato e permessi, commenti e cronologia, blocco ed eliminazione tracciata, etichette, milestone e assegnatari, ricerca nel repo e globale, allegati, UI lista issues con filtri (mockup 12);
- resta: UI di dettaglio (mockup 13) e di nuova issue, issues in sola lettura sui repo archiviati (R10), tabella regole→test I1–I11.

Fuori dalla v1: stati personalizzati, etichette e milestone di organizzazione, trasferimento di issues tra repo.

Priorità 5 di 9 nella v1. Mockup 12, 13 e una nuova schermata "New issue".

## Source references

- `.lmbrain-lite/milestones/M-05.md`
- Commit GIT-101…GIT-109 su `main`
- Tema di analisi "Issues: regole di prodotto" (I1–I11)

