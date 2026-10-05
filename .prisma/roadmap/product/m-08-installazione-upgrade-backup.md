---
{"horizon":"next","id":"OBJ-184e52c5-3841-4a77-9cea-fad781f5a813","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"reopen_reason":"Windows via WSL2 \"di prova\" con limiti dichiarati (W1, W2, 2026-10-05); D14 rivista.","schema_version":1,"title":"Installazione, aggiornamenti e backup","updated":"2026-10-05T13:20:00+00:00"}
---

# Installazione, aggiornamenti e backup

## Outcome

Un cliente installa GitStack su Linux con un comando (o su Windows via WSL2 per prove), lo aggiorna in sicurezza con rollback e fa backup e ripristino coerenti.

## Rationale

D1, D14, D18, D19: per un prodotto on-prem installazione, aggiornamenti e backup sono requisiti dal giorno uno; un aggiornamento fallito è un cliente perso.

## Scope

Installer definitivo Linux per Ubuntu Server e Pop!_OS 22.04/24.04 amd64 con preflight sui due profili hardware, un solo nodo, mirror/proxy configurabili, HTTPS di default con CA interna e verifica del nome host (N1–N6 in [[knowledge/topics/installazione-e-deploy]]); Windows via WSL2 di prova: script PowerShell, avvio al login, accesso dal PC e rete locale facoltativa (`-ShareOnNetwork`, Windows 11), solo test di installazione in CI (W1, W2); TLS (CA interna, Let's Encrypt, certificato del cliente); Postgres esterno; `gitstack upgrade` con backup, migrazioni e rollback; `gitstack backup/restore` coerente e backup giornaliero locale o S3; test CI di installazione, upgrade e ripristino; documentazione operativa. Base già esistente: installer v0 Ubuntu e chart Helm con Postgres esterno.

Fuori dalla v1: Debian, RHEL e derivate, arm64, più nodi, installazione e aggiornamenti air-gapped, supporto ufficiale (di produzione) su Windows.

Priorità 8 di 9 nella v1. Mockup 16.

## Source references

- `.lmbrain-lite/milestones/M-08.md`
- `deploy/README.md`
- Tema di analisi "Requisiti minimi e distribuzioni dell'installer definitivo" (N1–N6)
- Tema di analisi "Supporto Windows via WSL2 in produzione" (W1, W2)
