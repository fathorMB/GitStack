---
{"area":"technical-choices","id":"DOC-8d539b83-ec08-41ca-a680-1529555fc42a","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/architecture.md","source_id":"SRC-5d952707-85b7-46bf-bf7c-41dc9a714236"}],"tags":["architettura","servizi","k3s"],"title":"Architettura v1","updated":"2026-10-05T09:10:00+00:00"}
---

# Architettura v1

## Context

Architettura derivata da [[knowledge/topics/decisioni]] e confermata dall'implementazione di M-01/M-02. Dettagli verificati sui README del repo (`README.md`, `services/*/README.md`, `deploy/gitstack/README.md`).

## Confirmed decisions

Componenti (un container ciascuno, su k3s):

| Servizio | Ruolo | Stato nel repo |
|----------|-------|----------------|
| **gateway** (Go) | Unico ingresso API `/v1/*`: routing, autenticazione centralizzata, scope per rotta, rate limiting (aggancio no-op). Nessun database | realizzato |
| **identity** (Go) | Utenti, sessioni, token con scope, chiavi SSH, OIDC, organizzazioni, team, grant su risorse. Schema Postgres `identity` | realizzato, compreso il modello dei permessi; manca la gestione admin dei token degli agent |
| **git** (Go + binario `git`) | Push/pull HTTPS e SSH, lettura file/commit/diff, hook post-receive → `git.push` | storage dei repo e ciclo di vita realizzati; protocolli Git, permessi e `git.push` da fare |
| **core** (Go) | API di repo, issues, commenti, etichette, milestone, notifiche, webhook; consuma eventi. Schema `core` | contratto e schema dei repo realizzati, handler dei repo ancora 501 (GIT-67); issues da fare |
| **web** (React + TS, Vite) | SPA servita da nginx non privilegiato; `/api/*` inoltrato al gateway | shell, pagine di M-02, elenco/nuovo/vuoto dei repo |
| **postgres** | Dati strutturati, uno schema e un ruolo per servizio; nessun servizio legge le tabelle di un altro | nel chart |
| **nats** (JetStream) | Eventi tra servizi (vedi [[knowledge/topics/eventi]]) | nel chart |

Ingresso: Traefik v3 (k3s); `/` → web, `/api` → gateway con strip del prefisso.

Flussi chiave:

- **Push (previsto):** client → Traefik → git (auth chiesta a identity) → `git.push` su NATS → core collega commit e issues, notifiche e webhook.
- **Richiesta autenticata (realizzato):** client → gateway → `POST /internal/verify` su identity (con cache) → inoltro a core/identity con identità firmata HMAC. Dettagli in [[knowledge/topics/identita-e-sicurezza]].
- **Agente:** `gs` → gateway con token personale → API; skills descrivono i flussi tipici.

Contratti: API REST in OpenAPI come fonte unica (vedi [[knowledge/topics/contratto-api]]); eventi con schema versionato; modello dati predisposto per Pull Request (v1.1).

Monorepo: `api/` (contratto, modulo Go), `services/` (un modulo per servizio), `pkg/` (librerie condivise, es. `pkg/events`), `web/`, `cli/`, `client/` (go, ts generati), `skills/`, `deploy/`, `docs/`, `scripts/`.

## Open questions

- Il servizio git dovrà montare il PVC dei repo (`gitData.enabled`, oggi `false`): da attivare con M-03.

## Related topics

- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/stato-di-realizzazione]]

