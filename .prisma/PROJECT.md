---
title: GitStack
updated: 2026-10-18
---

# Project

## What it is

GitStack è un "mini GitHub" open source da installare on-prem con poca configurazione: repository Git remoti e Issues, gestibili da web UI, CLI o agenti di coding. A lungo termine diventa un piccolo cloud privato ("un piccolo Azure") dove si fanno build, deploy e hosting di applicazioni in container. Visione in [[knowledge/topics/visione]], decisioni in [[knowledge/topics/decisioni]], architettura in [[knowledge/topics/architettura]].

## Who uses it and why

Team e aziende che vogliono ospitare codice e issues sui propri server, senza cloud esterni. Gli agenti di coding sono utenti di prima classe: con la CLI `gs`, le skills e (dalla v1.1) un server MCP fanno tutto ciò che fa un umano dal browser.

## Stack

- Piattaforma: k3s incluso nell'installer (Linux: nella v1.0 supportati ufficialmente Ubuntu Server 22.04 e 24.04, Pop!_OS solo con avviso; Windows via WSL2 di prova).
- Backend: Go, pochi servizi (gateway, identity, git, core) + NATS JetStream.
- Dati: PostgreSQL (incluso o esterno), uno schema per servizio; repo Git su volume persistente.
- Frontend: React + TypeScript, usa solo l'API pubblica REST descritta in OpenAPI.
- Licenze: AGPL-3.0 per il server, Apache-2.0 per CLI, skills e client.

## Current state (2026-10-18)

M-01…M-07 completate (main al commit `4a946f2`). In corso i due blocchi rimasti della v1: **M-08** quasi completa (codice per N1–N10, W1, W2 e V5 presente; restano alcune prove) e **M-09** (fatti mirror in pull e import, mirror in push, revisione di sicurezza con correzione dei problemi alti, test di fumo, lista di controllo della rc, job di rilascio provato su un tag di prova). Il passaggio del codice di GitStack su homehub è in due tappe: la copia di backup c'è; la casa su GitStack (tappa 2) arriva quando sono vere le condizioni 2–5 (la 1 è tolta il 2026-10-18) e oggi manca solo la 2, un ripristino senza passi a mano. Dopo la tappa 2 il dogfooding dura almeno 2 settimane con 4 condizioni esplicite (scelta del 2026-10-18). Obiettivi, non vincoli: tappa 2 fra il 23 e il 25/10/2026, v1.0 fra l'8 e il 10/11/2026. La v2 (CI in stile GitHub Actions, registry, release) e la v3 (hosting di app e container) sono analizzate e restano dopo v1.0 e v1.1. Dettagli in [[knowledge/topics/stato-di-realizzazione]], [[knowledge/topics/rilascio-v1]], [[knowledge/topics/mirror-in-pull]], [[knowledge/topics/ci-registry-release]], [[knowledge/topics/hosting-app]]. I task di sviluppo sono tracciati in GalaxyLab (item GIT-n); con il dogfooding passano gradualmente a GitStack (V7).

## Constraints

- Installazione con un comando, configurazione minima.
- API-first: nessuna funzione esclusiva della UI.
- Tutte le dipendenze compatibili con AGPL-3.0.
- Sviluppo da operatore + agenti AI: contratti espliciti (OpenAPI, schemi DB, eventi versionati) e test automatici forti.

## Out of scope (v1)

Pull Request (v1.1, già previste nel modello dati), bacheca Kanban, CI/build, registry container e release (v2), deploy di app (v3), server MCP (v1.1), LDAP.
