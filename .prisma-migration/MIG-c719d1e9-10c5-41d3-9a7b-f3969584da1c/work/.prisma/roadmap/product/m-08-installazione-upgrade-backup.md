---
{"horizon":"next","id":"OBJ-184e52c5-3841-4a77-9cea-fad781f5a813","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"schema_version":1,"title":"Installazione, aggiornamenti e backup","updated":"2026-09-30T21:15:06.328707700+00:00"}
---

# Installazione, aggiornamenti e backup

## Outcome

Un cliente installa GitStack su Linux o Windows (WSL2) con un comando, lo aggiorna in sicurezza con rollback e fa backup e ripristino coerenti.

## Rationale

D1, D14, D18, D19: per un prodotto on-prem installazione, aggiornamenti e backup sono requisiti dal giorno uno; un aggiornamento fallito è un cliente perso.

## Scope

Installer definitivo Linux (Ubuntu, Debian, RHEL) con prerequisiti; Windows via WSL2 con avvio automatico, rete e test di riavvio; TLS (CA interna, Let's Encrypt, certificato del cliente); Postgres esterno; `gitstack upgrade` con backup, migrazioni e rollback; `gitstack backup/restore` coerente e backup giornaliero locale o S3; test CI di installazione, upgrade e ripristino; documentazione operativa. Base già esistente: installer v0 Ubuntu e chart Helm con Postgres esterno.

Priorità 8 di 9 nella v1. Mockup 16.

## Source references

- `.lmbrain-lite/milestones/M-08.md`
- `deploy/README.md`

