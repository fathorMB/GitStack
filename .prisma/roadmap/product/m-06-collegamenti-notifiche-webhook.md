---
{"horizon":"next","id":"OBJ-60448747-d91d-4e43-8de7-fa718dadf250","knowledge":["DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"M-06 completata: famiglia C1–C9 coperta da test.","schema_version":1,"title":"Collegamenti, notifiche e webhook","updated":"2026-10-11T22:20:26.313095100+00:00"}
---


# Collegamenti, notifiche e webhook

## Outcome

Codice e issues sono collegati (`#12`, `fixes #12` chiude la issue), gli utenti ricevono notifiche in-app ed email, e sistemi esterni ricevono webhook firmati.

**Stato: completata** (main al commit `e708707`, 2026-10-12): famiglia C1–C9 tutta coperta da test, con end-to-end in `TestM06E2E`. Dettagli in [[knowledge/topics/stato-di-realizzazione]].

## Rationale

D10 e D11: il consumo degli eventi `git.push` rende il bus la spina dorsale del prodotto e prepara CI e deploy futuri.

## Scope

Regole in [[knowledge/topics/collegamenti-notifiche-webhook]] (C1–C9). Fuori dalla v1: risposta via email, formato webhook compatibile GitHub, parole chiave in italiano, riepiloghi email periodici.

Priorità 6 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-06.md`
- `docs/rules-coverage.md` (famiglia C)

