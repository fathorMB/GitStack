---
id: M-06
title: "Collegamenti, notifiche e webhook"
status: approved
priority: 6
created: 2026-09-27
updated: "2026-09-27"
---
# M-06 — Collegamenti, notifiche e webhook

## Outcome

Codice e issues sono collegati (#12, fixes #12 chiude la issue), gli utenti ricevono notifiche in-app ed email, e sistemi esterni ricevono webhook firmati.

## Tasks

- [ ] T-01 Riferimenti #N e @utente resi come link nel Markdown
- [ ] T-02 Consumo evento git.push: commit collegati alle issue citate
- [ ] T-03 Chiusura automatica con fixes/closes #N sul branch principale
- [ ] T-04 Notifiche in-app (menzioni, assegnazioni, commenti su issue seguite)
- [ ] T-05 Notifiche email via SMTP configurabile
- [ ] T-06 Webhook per repo e organizzazione: eventi selezionabili, firma HMAC, tentativi ripetuti, log delle consegne
- [ ] T-07 UI: centro notifiche e configurazione webhook

## Notes

- Mockup: `design/mockups-v1/` schermate 05 Notifications, 11 Settings · Webhooks, 13 Issue detail (commit collegati, chiusura automatica).
