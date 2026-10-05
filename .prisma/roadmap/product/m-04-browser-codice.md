---
{"horizon":"next","id":"OBJ-affcf73c-61dd-4152-b847-5ca7d47d0db2","knowledge":["DOC-ef8572eb-11a9-4ab0-ac04-ff006b41bdc4","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-04fbc589-a144-403b-99e0-13e541fac16a"],"reopen_reason":"Regole B1–B7 sul browser del codice (tema consolidato il 2026-10-05).","schema_version":1,"title":"Browser del codice","updated":"2026-10-05T09:30:00+00:00"}
---

# Browser del codice

## Outcome

Dal browser si naviga un repo come su GitHub: file, README, branch, tag, storico commit e diff.

## Rationale

D9: il browser web del codice fa parte del minimo per un prodotto venduto a team; le stesse letture sono disponibili via API agli agenti (D7).

## Scope

API di lettura (albero, contenuto, branch, tag, storico, dettaglio commit con diff); UI con README renderizzato, evidenziazione della sintassi, selettore branch/tag, storico, diff, lista e creazione repo per utente e organizzazione.

Regole in [[knowledge/topics/browser-codice]] (B1–B7): limiti di visualizzazione con Download; Markdown in stile GitHub protetto (lo stesso di issues e commenti); raw e archivi ZIP/tar.gz con login o token; storico per file e blame; "Go to file" e ricerca testuale nel singolo repo; diff con limiti progressivi, vista unificata o affiancata e "ignora spazi"; pagina Tags.

Fuori dalla v1: Mermaid e formule, link temporanei senza login, ricerca indicizzata su tutta l'installazione, release (in v2).

Priorità 4 di 9 nella v1. Mockup 07–10 e nuove schermate Blame e Tags.

## Source references

- `.lmbrain-lite/milestones/M-04.md`
- Tema di analisi "Browser del codice: regole di prodotto" (B1–B7)
