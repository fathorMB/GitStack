---
{"horizon":"next","id":"OBJ-60448747-d91d-4e43-8de7-fa718dadf250","knowledge":["DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"schema_version":1,"title":"Collegamenti, notifiche e webhook","updated":"2026-09-30T21:15:06.326692700+00:00"}
---

# Collegamenti, notifiche e webhook

## Outcome

Codice e issues sono collegati (`#12`, `fixes #12` chiude la issue), gli utenti ricevono notifiche in-app ed email, e sistemi esterni ricevono webhook firmati.

## Rationale

D10 e D11: il consumo degli eventi `git.push` rende il bus la spina dorsale del prodotto e prepara CI e deploy futuri.

## Scope

Riferimenti `#N` e `@utente` come link; commit collegati alle issue citate; chiusura automatica con fixes/closes sul branch principale; notifiche in-app (menzioni, assegnazioni, commenti su issue seguite) ed email via SMTP configurabile; webhook per repo e organizzazione con eventi selezionabili, firma HMAC, tentativi ripetuti e log delle consegne.

Priorità 6 di 9 nella v1. Mockup 05, 11, 13.

## Source references

- `.lmbrain-lite/milestones/M-06.md`

