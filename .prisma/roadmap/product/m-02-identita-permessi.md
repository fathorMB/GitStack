---
{"horizon":"next","id":"OBJ-c79ed133-8339-4a82-9f91-6b100a636da1","knowledge":["DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"Milestone completata: esce dall'orizzonte now, come M-03 e M-04.","schema_version":1,"title":"Identità, organizzazioni e permessi (completata)","updated":"2026-10-11T22:20:26.309466200+00:00"}
---


# Identità, organizzazioni e permessi (completata)

## Outcome

Utenti e agenti si autenticano (password, token con scope, chiavi SSH, OIDC esterno) e i permessi su organizzazioni, team e risorse generiche sono applicati ovunque.

**Stato: completata.** Dettagli in [[knowledge/topics/stato-di-realizzazione]]. Resta in roadmap solo perché la roadmap non ha un orizzonte per il lavoro concluso.

## Rationale

D5 e D15: identità pronta all'uso, token con permessi limitati per gli agenti, permessi modellati su risorse generiche.

## Scope

Utenti locali, sessioni, token, chiavi SSH, OIDC, organizzazioni e team, modello dei permessi P1–P7, utenti agent gestiti dall'amministratore (GIT-78, GIT-79), API dell'accesso effettivo (GIT-95). Famiglia P interamente coperta da test, compresa P7 via `gs`.

Priorità 2 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-02.md`
- `docs/rules-coverage.md` (famiglia P)

