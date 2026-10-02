---
id: M-03
title: Hosting dei repository Git
status: approved
priority: 3
created: 2026-09-27
updated: "2026-09-27"
---
# M-03 — Hosting dei repository Git

## Outcome

Si crea un repo da UI o API e ci si fa clone, push e pull via HTTPS (token) e SSH, con permessi e visibilità rispettati; ogni push pubblica un evento.

## Tasks

- [ ] T-01 Servizio git: storage dei repo su volume persistente, creazione e cancellazione
- [ ] T-02 API repo: creazione, impostazioni, visibilità (privato/interno/pubblico), owner utente o organizzazione
- [ ] T-03 Push/pull via HTTPS smart protocol con autenticazione a token
- [ ] T-04 Server SSH integrato per push/pull con chiavi utente
- [ ] T-05 Controllo permessi su ogni operazione Git
- [ ] T-06 Hook post-receive che pubblica l'evento git.push su NATS
- [ ] T-07 Modello dati predisposto per Pull Request (v1.1)
- [ ] T-08 Test end-to-end con client git reale: clone, push, pull, accesso negato

## Notes

- Mockup: `design/mockups-v1/` schermate 03 Repositories, 04 New repository, 06 Empty repository.
