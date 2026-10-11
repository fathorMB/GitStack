---
title: GitStack
updated: 2026-10-12
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

## Current state (2026-10-12)

M-01…M-07 completate (main al commit `e708707`). In corso i due blocchi rimasti della v1: **M-08** (fatti installer su Ubuntu 24.04 con HTTPS, `gitstack` status/backup/restore/upgrade con rollback; restano Ubuntu 22.04 e Pop!_OS, WSL2 di prova, backup su S3, Postgres esterno, proxy e mirror aziendali, firme cosign, aggiornamento di k3s, test di installazione in CI — regole N1–N10) e **M-09** (primo pezzo: mirror in pull e import una tantum, regole MP1–MP8; poi passaggio del codice di GitStack su homehub in due tappe, copia di backup e poi casa su GitStack a cinque condizioni; poi il resto di V1–V8). La v2 (CI in stile GitHub Actions, registry, release) è analizzata (CI1–CI7, RG1–RG2, REL1) e resta dopo v1.0 e v1.1. Dettagli in [[knowledge/topics/stato-di-realizzazione]], [[knowledge/topics/rilascio-v1]], [[knowledge/topics/mirror-in-pull]], [[knowledge/topics/ci-registry-release]]. I task di sviluppo sono tracciati in GalaxyLab (item GIT-n); con il dogfooding passano gradualmente a GitStack (V7).

## Constraints

- Installazione con un comando, configurazione minima.
- API-first: nessuna funzione esclusiva della UI.
- Tutte le dipendenze compatibili con AGPL-3.0.
- Sviluppo da operatore + agenti AI: contratti espliciti (OpenAPI, schemi DB, eventi versionati) e test automatici forti.

## Out of scope (v1)

Pull Request (v1.1, già previste nel modello dati), bacheca Kanban, CI/build, registry container e release (v2), deploy di app (v3), server MCP (v1.1), LDAP.
