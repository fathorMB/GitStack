---
{"horizon":"now","id":"OBJ-184e52c5-3841-4a77-9cea-fad781f5a813","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"Regole N7–N10 confermate dall'operatore il 2026-10-12; N10 aggiunge l'aggiornamento di k3s all'elenco del CEO.","schema_version":1,"title":"Installazione, aggiornamenti e backup","updated":"2026-10-11T23:05:17.558176600+00:00"}
---


# Installazione, aggiornamenti e backup

## Outcome

Un cliente installa GitStack su Linux con un comando (o su Windows via WSL2 per prove), anche in una rete aziendale chiusa e con il proprio Postgres, lo aggiorna in sicurezza con rollback (k3s compreso) e fa backup e ripristino coerenti, anche su S3.

## Rationale

D1, D14, D18, D19: per un prodotto on-prem installazione, aggiornamenti e backup sono requisiti dal giorno uno; un aggiornamento fallito è un cliente perso. N10 tiene le patch di sicurezza di k3s sotto GitStack (V4).

## Scope

Requisiti N1–N10, W1, W2 e V5 in [[knowledge/topics/installazione-e-deploy]].

Avanzamento al commit `e708707` (2026-10-12):
- **fatto:** installer su Ubuntu Server 24.04 con preflight (N1, N6), HTTPS con CA interna, Let's Encrypt o certificato del cliente (N5), comando `gitstack` con `status`, `backup` (anche cifrato), `restore`, `upgrade` con backup preventivo e rollback, timer di backup giornaliero locale, guida operativa;
- **resta:** Ubuntu 22.04 e Pop!_OS 22.04/24.04 (N2); Windows via WSL2 di prova (W1, W2); backup su S3 con cifratura obbligatoria e `restore --from s3` (N7); Postgres esterno con supporto pieno, PostgreSQL 15–17 (N8); proxy, mirror di immagini e download, CA aziendale (N4, N9); verifica delle firme cosign (V5); aggiornamento di k3s, patch automatiche e minori con `--k3s` (N10); test in CI di installazione, aggiornamento (anche di k3s) e ripristino su tutti i sistemi supportati.

Fuori dalla v1: Debian, RHEL e derivate, arm64, più nodi, air-gapped, supporto di produzione su Windows, alta disponibilità del Postgres esterno (responsabilità del cliente).

Priorità 8 di 9 nella v1. Mockup 16.

## Source references

- `.lmbrain-lite/milestones/M-08.md`
- `deploy/README.md`, `docs/operations.md`
- Temi di analisi "Requisiti minimi e distribuzioni dell'installer definitivo" (N1–N6) e "Supporto Windows via WSL2 in produzione" (W1, W2)
- Scelte dell'operatore del 2026-10-12 (N7–N10)

