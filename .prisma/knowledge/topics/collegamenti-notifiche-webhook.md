---
{"area":"requirements","id":"DOC-6db23bbe-fbf2-4913-a207-711865af66f6","related":["TOP-062637bf-9acd-47cb-a089-3fb3bccee486","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","OBJ-60448747-d91d-4e43-8de7-fa718dadf250"],"schema_version":1,"sources":[],"tags":["collegamenti","notifiche","email","webhook","sicurezza","m-06"],"title":"Collegamenti, notifiche e webhook: regole di prodotto","updated":"2026-10-05T09:10:00+00:00"}
---

# Collegamenti, notifiche e webhook: regole di prodotto

## Context

Regole per M-06 confermate dall'operatore il 2026-10-05 nel tema di analisi "Collegamenti, notifiche e webhook: regole di prodotto". Completano D10 e D11 ([[knowledge/topics/decisioni]]), le convenzioni di [[knowledge/topics/eventi]] e le regole di [[knowledge/topics/issues]] (I1, I2, I8) e [[knowledge/topics/repository-git]] (R4).

## Confirmed decisions

| # | Regola |
|---|---|
| C1 | **Riferimenti tra repo:** `#12` = repo corrente, `owner/repo#12` = altro repo. Link e "referenced from …" visibili solo a chi vede entrambi i repo. `fixes owner/repo#12` chiude solo se chi fa il push ha `write` sul repo della issue. |
| C2 | **Chiusura via commit:** parole `close(s/d)`, `fix(es/ed)`, `resolve(s/d)` senza distinzione di maiuscole. Su qualsiasi branch il commit crea solo il collegamento; la issue si chiude come *completata* quando il commit entra nel branch principale (push diretto o, dalla v1.1, merge di PR), con traccia "closed by commit <sha>". Una issue riaperta non viene richiusa dallo stesso commit. |
| C3 | **Chi segue cosa:** autore, assegnatari, commentatori e menzionati seguono la issue in automatico; Subscribe/Unsubscribe. Notifiche per commenti, chiusura/riapertura, assegnazioni, commit collegati. Watch del repo: *Partecipando* (default), *Tutto*, *Ignora*. Nessuna notifica per le proprie azioni, anche via token. |
| C4 | **Agenti:** stessa casella e stesse regole delle persone, lette via API e `gs notification list --json` con filtro per motivo; niente email agli agenti; webhook per reagire subito. |
| C5 | **Email:** SMTP facoltativo (senza, solo in-app). Preferenze per tipo; default email per menzioni e assegnazioni. Invio immediato con raggruppamento di pochi secondi per issue. Nessuna risposta via email nella v1. |
| C6 | **Webhook:** di repo (admin del repo) e di organizzazione (owner). Eventi `push`, `issues`, `issue_comment`, `repository`. Formato **GitStack** documentato e versionato, ispirato a GitHub; header `X-GitStack-Event`, `X-GitStack-Delivery`, `X-GitStack-Signature` (HMAC-SHA256). |
| C7 | **Consegna:** su 5xx, errore di rete o timeout di 10 s fino a 8 tentativi in ~24 ore con attesa crescente; `410 Gone` ferma la consegna. Dopo 3 giorni di fallimenti consecutivi il webhook si disattiva e chi lo gestisce viene notificato. Log delle consegne con "Redeliver" per 30 giorni. |
| C8 | **Protezione SSRF:** ammessa la rete aziendale; sempre bloccati loopback, indirizzi interni del cluster k3s e link-local (`169.254.0.0/16`), anche dopo redirect e risoluzione DNS. Liste ammesse/vietate dell'amministratore dell'installazione. Risposta nel log troncata a 4 KB. |
| C9 | **Conservazione notifiche:** lette eliminate dopo 90 giorni, non lette conservate; spariscono se si perde l'accesso al repo; gestione manuale anche via API e `gs`. |

## Fuori dalla v1

Risposta via email (C5), formato webhook compatibile GitHub (C6, estensione possibile per `push`), parole chiave in italiano (C2), riepiloghi email periodici.

## Related topics

- [[knowledge/topics/issues]]
- [[knowledge/topics/eventi]]
- [[knowledge/topics/decisioni]]
- [[knowledge/topics/installazione-e-deploy]]
