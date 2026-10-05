---
{"horizon":"next","id":"OBJ-60448747-d91d-4e43-8de7-fa718dadf250","knowledge":["DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-6db23bbe-fbf2-4913-a207-711865af66f6"],"reopen_reason":"Regole C1–C9 su collegamenti, notifiche e webhook (tema consolidato il 2026-10-05).","schema_version":1,"title":"Collegamenti, notifiche e webhook","updated":"2026-10-05T09:10:00+00:00"}
---

# Collegamenti, notifiche e webhook

## Outcome

Codice e issues sono collegati (`#12`, `fixes #12` chiude la issue), gli utenti ricevono notifiche in-app ed email, e sistemi esterni ricevono webhook firmati.

## Rationale

D10 e D11: il consumo degli eventi `git.push` rende il bus la spina dorsale del prodotto e prepara CI e deploy futuri.

## Scope

Riferimenti `#N` e `@utente` come link; commit collegati alle issue citate; chiusura automatica con fixes/closes sul branch principale; notifiche in-app (menzioni, assegnazioni, commenti su issue seguite) ed email via SMTP configurabile; webhook per repo e organizzazione con eventi selezionabili, firma HMAC, tentativi ripetuti e log delle consegne.

Regole in [[knowledge/topics/collegamenti-notifiche-webhook]] (C1–C9): riferimenti tra repo `owner/repo#n` nel rispetto della visibilità; parole chiave di GitHub con chiusura sul branch principale; partecipanti che seguono in automatico e Watch a tre livelli; agenti con la stessa casella via API e `gs`; SMTP facoltativo con preferenze per tipo; webhook di repo e organizzazione in formato GitStack documentato, con tentativi crescenti, disattivazione automatica e protezione SSRF; notifiche lette conservate 90 giorni.

Fuori dalla v1: risposta via email, formato webhook compatibile GitHub, parole chiave in italiano, riepiloghi email periodici.

Priorità 6 di 9 nella v1. Mockup 05, 07, 11, 13 e una nuova schermata "Notification settings".

## Source references

- `.lmbrain-lite/milestones/M-06.md`
- Tema di analisi "Collegamenti, notifiche e webhook: regole di prodotto" (C1–C9)
