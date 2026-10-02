---
id: M-08
title: "Installazione, aggiornamenti e backup"
status: approved
priority: 8
created: 2026-09-27
updated: "2026-09-27"
---
# M-08 — Installazione, aggiornamenti e backup

## Outcome

Un cliente installa GitStack su Linux o Windows (WSL2) con un comando, lo aggiorna in sicurezza con rollback e fa backup e ripristino coerenti.

## Tasks

- [ ] T-01 Installer definitivo Linux (Ubuntu, Debian, RHEL) con controlli prerequisiti
- [ ] T-02 Installer Windows via WSL2: avvio automatico, rete e porte esposte, test di riavvio
- [ ] T-03 TLS: CA interna auto-generata, opzione Let's Encrypt, opzione certificato del cliente
- [ ] T-04 Opzione Postgres esterno del cliente
- [ ] T-05 gitstack upgrade: download, backup, migrazioni, aggiornamento servizi, rollback
- [ ] T-06 gitstack backup/restore: archivio coerente DB + repo + configurazione
- [ ] T-07 Backup pianificato giornaliero con conservazione ultimi N, destinazione locale o S3
- [ ] T-08 Test CI: installazione pulita, upgrade dalla versione precedente, ripristino da backup
- [ ] T-09 Documentazione di installazione e operazioni

## Notes

- Mockup: `design/mockups-v1/` schermata 16 Admin · System (servizi, backup, upgrade).
