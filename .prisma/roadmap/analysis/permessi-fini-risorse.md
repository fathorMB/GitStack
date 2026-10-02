---
{"depends_on":[],"id":"TOP-4a11694d-e261-4934-bafb-f17ccc6d1726","knowledge":["DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"reopen_reason":"Consolidazione dopo P1–P7.","schema_version":1,"state":"consolidated","title":"Permessi fini sulle risorse e visibilità dei repo","updated":"2026-09-30T21:34:14.592084800+00:00"}
---


# Permessi fini sulle risorse e visibilità dei repo

## Expected learning

Regole di prodotto chiare su chi può leggere, scrivere e amministrare una risorsa, prima che il modello dei permessi (GIT-38) e l'hosting Git (M-03) vengano realizzati.

## Scelte confermate dall'operatore (2026-09-30)

- **P1 — Ruoli di organizzazione:** gli `owner` hanno sempre `admin` su tutte le risorse dell'organizzazione; i `member` accedono alle risorse private solo con un grant (diretto o via team) o per effetto della visibilità.
- **P2 — Nessun accesso anonimo:** per vedere qualsiasi repo serve un account sull'installazione.
- **P3 — Visibilità privato/interno:** privato = owner (o proprietario) e chi ha un grant; interno = lettura per tutti gli utenti con account sull'installazione. "Pubblico" eliminato. La scrittura richiede sempre `write` (grant) o il ruolo owner/proprietario.
- **P4 — Agenti limitati con utenti agent dedicati:** il token vale su ciò che vede il proprietario, ristretto solo dagli scope; niente token per singola risorsa nella v1.
- **P5 — Utenti agent gestiti dall'admin:** solo l'admin dell'installazione crea ed elimina utenti agent e può crearne e revocarne i token senza login al loro posto; gli agent non hanno password.
- **P6 — Repo personali ammessi:** il proprietario ne è `admin`; stesse regole di visibilità e grant.
- **P7 — Default privato:** un nuovo repo è privato salvo scelta esplicita, anche se creato via API o `gs`.

## Consolidation summary

Permesso effettivo di un utente su una risorsa = il più alto tra: `admin` se è amministratore dell'installazione, owner dell'organizzazione proprietaria o proprietario del repo personale; il ruolo del grant diretto o via team; `read` se la risorsa è interna. Un token applica in più i propri scope. Senza account nessun accesso. Riportato in [[knowledge/topics/identita-e-sicurezza]], in D9 ([[knowledge/topics/decisioni]]) e negli obiettivi M-02 e M-03. Mockup 03 e 04 da aggiornare (opzione Public).

