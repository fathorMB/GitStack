---
{"horizon":"next","id":"OBJ-affcf73c-61dd-4152-b847-5ca7d47d0db2","knowledge":["DOC-ef8572eb-11a9-4ab0-ac04-ff006b41bdc4","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-04fbc589-a144-403b-99e0-13e541fac16a","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"M-04 completata secondo il codice di main (GIT-80…GIT-94, GIT-116, GIT-117, GIT-120, GIT-121).","schema_version":1,"title":"Browser del codice (completata)","updated":"2026-10-05T19:53:06.226519600+00:00"}
---


# Browser del codice (completata)

## Outcome

Dal browser si naviga un repo come su GitHub: file, README, branch, tag, storico commit e diff.

**Stato: completata** secondo il codice di `main` al commit `1587133` (2026-10-05): pagine Code, file, History, Blame, Raw, commit e diff, Tags e Search code, con test end-to-end sullo stack e sulla VM. Dettagli in [[knowledge/topics/stato-di-realizzazione]].

## Rationale

D9: il browser web del codice fa parte del minimo per un prodotto venduto a team; le stesse letture sono disponibili via API agli agenti (D7).

## Scope

API di lettura (albero, contenuto, branch, tag, storico, dettaglio commit con diff); UI con README renderizzato, evidenziazione della sintassi, selettore branch/tag, storico, diff, lista e creazione repo per utente e organizzazione.

Regole in [[knowledge/topics/browser-codice]] (B1–B7): B1, B2, B3, B6, B7 coperte da test; B4 e B5 parziali solo per la parte `gs` (storico, blame e ricerca da CLI arrivano con M-07).

Fuori dalla v1: Mermaid e formule, link temporanei senza login, ricerca indicizzata su tutta l'installazione, release (in v2).

Priorità 4 di 9 nella v1. Mockup 07–10, 22 (Blame) e 23 (Tags).

## Source references

- `.lmbrain-lite/milestones/M-04.md`
- `docs/rules-coverage.md` (famiglia B)
- Tema di analisi "Browser del codice: regole di prodotto" (B1–B7)

