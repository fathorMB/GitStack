---
title: Architettura v1
updated: 2026-09-27
---

# Architettura v1

Bozza derivata da [[decisions]]; si raffina nelle milestone. Visione in [[vision]].

## Componenti (un container ciascuno, su k3s)

| Servizio | Ruolo |
|----------|-------|
| **gateway** (Go) | Unico punto d'ingresso API: routing, verifica token, limiti di richieste. Dietro Traefik. |
| **identity** (Go) | Utenti locali, sessioni, token personali con scope, chiavi SSH, login OIDC esterno, organizzazioni, team, ruoli. |
| **git** (Go + binario `git`) | Push/pull HTTPS e SSH, lettura di file/commit/diff, hook post-receive che pubblica eventi `git.push`. Repo su volume persistente. |
| **core** (Go) | API di repo (metadati), issues, commenti, etichette, milestone, notifiche, webhook. Consuma eventi (es. `fixes #12` chiude la issue). |
| **web** (React + TS) | Interfaccia web, file statici; parla solo con il gateway. |
| **postgres** | Dati strutturati; uno schema per servizio, nessun servizio legge le tabelle di un altro. |
| **nats** (JetStream) | Eventi tra servizi: `git.push`, `issue.*`, `repo.*`; base futura per CI e deploy. |

## Flussi chiave

- **Push:** client → Traefik → git (auth chiesta a identity) → `git.push` su NATS → core collega commit e issues, invia notifiche e webhook.
- **Agente:** `gs` (CLI) → gateway con token personale → API core/git. Skills descrivono i flussi tipici.

## Contratti

- API pubblica REST descritta in OpenAPI (fonte unica: da essa si generano client TS della UI, client della CLI e, in v1.1, server MCP).
- Eventi con schema versionato (nome, versione, payload JSON).
- Modello dati con predisposizione per Pull Request (v1.1).

## Installazione

Installer unico: prepara k3s (Linux, oppure dentro WSL2 su Windows), installa i servizi, genera CA interna e certificati, crea l'utente admin.
