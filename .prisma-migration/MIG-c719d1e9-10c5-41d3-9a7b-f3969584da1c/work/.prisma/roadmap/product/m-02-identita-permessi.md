---
{"horizon":"now","id":"OBJ-c79ed133-8339-4a82-9f91-6b100a636da1","knowledge":["DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"schema_version":1,"title":"Identità, organizzazioni e permessi","updated":"2026-09-30T21:15:06.322410200+00:00"}
---

# Identità, organizzazioni e permessi

## Outcome

Utenti e agenti si autenticano (password, token con scope, chiavi SSH, OIDC esterno) e i permessi su organizzazioni, team e risorse generiche sono applicati ovunque.

## Rationale

D5 e D15: identità pronta all'uso dopo l'installazione, token con permessi limitati per gli agenti, permessi modellati su risorse generiche così che repo, app e database futuri si aggancino senza riscritture. È la base di tutte le milestone successive.

## Scope

Già realizzato: utenti locali e password, admin al primo avvio con cambio password obbligatorio, sessioni, token con scope e scadenza, chiavi SSH, login OIDC (Entra ID, Google, Keycloak), organizzazioni e team, autenticazione centralizzata nel gateway, UI di login, profilo, token, chiavi e organizzazioni.

Resta: modello dei permessi sulle risorse (lettura/scrittura/admin per utente o team) con verifica tramite identity; test di sicurezza di accesso negato senza permesso.

Priorità 2 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-02.md`
- `services/identity/README.md`, `services/gateway/README.md`, commit GIT-29…GIT-60

