# services/

Servizi backend Go di GitStack, un container ciascuno, orchestrati su k3s dietro Traefik. Architettura completa: `.lmbrain-lite/knowledge/architecture.md`.

| Servizio | Ruolo |
|----------|-------|
| `gateway/` | Unico punto d'ingresso API: routing, verifica token, rate limiting. |
| `identity/` | Utenti locali, sessioni, token con scope, chiavi SSH, login OIDC, organizzazioni, team, ruoli. |
| `git/` | Push/pull HTTPS e SSH, lettura file/commit/diff, hook che pubblica eventi su NATS. |
| `core/` | API di repo, issues, commenti, etichette, milestone, notifiche, webhook. |

Ogni cartella è un modulo Go a sé (vedi `go.work` in radice), oggi solo lo scheletro (`go.mod`, nessun codice di servizio): il codice arriva con le milestone dedicate (M-01 T-04/T-05 e successive).

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice e la motivazione nel README principale).
