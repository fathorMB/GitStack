---
title: GitStack
updated: 2026-09-27
---

# Project

## What it is

GitStack è un "mini GitHub" open source (AGPL-3.0) da installare on-prem con poca configurazione. La v1 offre repository Git remoti e Issues. A lungo termine diventa un piccolo cloud privato ("un piccolo Azure") dove si fanno build, deploy e hosting di applicazioni in container, tutto gestibile da web UI. Visione in [[vision]], architettura in [[architecture]], decisioni in [[decisions]].

## Who uses it and why

Team e aziende che vogliono ospitare codice e issues sui propri server, senza cloud esterni. Utenti di prima classe sono anche gli **agenti di coding**: tramite la CLI `gs`, le skills e (dopo) un server MCP possono fare tutto ciò che fa un umano dal browser.

## Stack and how to run it

- Piattaforma: k3s incluso nell'installer (Linux; Windows via WSL2).
- Backend: Go, pochi servizi (gateway/identità, Git, API core) + NATS JetStream come bus eventi.
- Dati: PostgreSQL (incluso o esterno); repo Git su volume persistente.
- Frontend: React + TypeScript, usa solo l'API pubblica REST + OpenAPI.
- Come eseguirlo: da definire nella prima milestone ("scheletro che cammina").

## Constraints

- Installazione con un comando, configurazione minima.
- API-first: nessuna funzione esclusiva della UI.
- Tutte le dipendenze compatibili con AGPL-3.0.
- Sviluppo da operatore + agenti AI: contratti espliciti (OpenAPI, schemi DB) e test automatici.

## Out of scope (v1)

Pull Request (v1.1, ma previste nel modello dati), bacheca Kanban, CI/build, registry container, deploy di app, server MCP, LDAP.
