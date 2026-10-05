---
title: GitStack
updated: 2026-10-05
---

# Project

## What it is

GitStack è un "mini GitHub" open source da installare on-prem con poca configurazione: repository Git remoti e Issues, gestibili da web UI, CLI o agenti di coding. A lungo termine diventa un piccolo cloud privato ("un piccolo Azure") dove si fanno build, deploy e hosting di applicazioni in container. Visione in [[knowledge/topics/visione]], decisioni in [[knowledge/topics/decisioni]], architettura in [[knowledge/topics/architettura]].

## Who uses it and why

Team e aziende che vogliono ospitare codice e issues sui propri server, senza cloud esterni. Gli agenti di coding sono utenti di prima classe: con la CLI `gs`, le skills e (dalla v1.1) un server MCP fanno tutto ciò che fa un umano dal browser.

## Stack

- Piattaforma: k3s incluso nell'installer (Linux: Ubuntu Server e Pop!_OS; Windows via WSL2 di prova).
- Backend: Go, pochi servizi (gateway, identity, git, core) + NATS JetStream.
- Dati: PostgreSQL (incluso o esterno), uno schema per servizio; repo Git su volume persistente.
- Frontend: React + TypeScript, usa solo l'API pubblica REST descritta in OpenAPI.
- Licenze: AGPL-3.0 per il server, Apache-2.0 per CLI, skills e client.

## Current state (2026-10-05)

M-01 "Scheletro che cammina" completata; M-02 "Identità, organizzazioni e permessi" quasi completata (manca la gestione admin dei token degli agent); M-03 "Hosting Git" in corso (storage, nomi, modelli e UI fatti; API pubblica e protocolli Git da fare); M-04…M-09 da fare. Regole di prodotto consolidate per M-03…M-09 (repo, browser, issues, collegamenti e notifiche, CLI e skills, installer, Windows di prova, rilascio). Dettagli in [[knowledge/topics/stato-di-realizzazione]]. I task di sviluppo sono tracciati in GalaxyLab (item GIT-n); con il dogfooding passano gradualmente a GitStack (V7).

## Constraints

- Installazione con un comando, configurazione minima.
- API-first: nessuna funzione esclusiva della UI.
- Tutte le dipendenze compatibili con AGPL-3.0.
- Sviluppo da operatore + agenti AI: contratti espliciti (OpenAPI, schemi DB, eventi versionati) e test automatici forti.

## Out of scope (v1)

Pull Request (v1.1, già previste nel modello dati), bacheca Kanban, CI/build, registry container, deploy di app, server MCP, LDAP.
