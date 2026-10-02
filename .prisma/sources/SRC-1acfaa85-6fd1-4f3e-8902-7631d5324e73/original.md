---
id: M-02
title: "Identità, organizzazioni e permessi"
status: approved
priority: 2
created: 2026-09-27
updated: "2026-09-27"
---
# M-02 — Identità, organizzazioni e permessi

## Outcome

Utenti e agenti si autenticano (password, token con scope, chiavi SSH, OIDC esterno) e i permessi su organizzazioni, team e risorse generiche sono applicati ovunque.

## Tasks

- [ ] T-01 Servizio identity: utenti locali, password sicure, sessioni web
- [ ] T-02 Creazione utente admin al primo avvio
- [ ] T-03 Token personali con scope e scadenza (per CLI e agenti)
- [ ] T-04 Gestione chiavi SSH degli utenti
- [ ] T-05 Login esterno OIDC configurabile (Entra ID, Google, Keycloak)
- [ ] T-06 Organizzazioni, team e membri
- [ ] T-07 Modello permessi su risorse generiche (lettura/scrittura/admin) con verifica centralizzata nel gateway
- [ ] T-08 UI: login, profilo, token, chiavi SSH, gestione organizzazioni e team
- [ ] T-09 Test di sicurezza: accesso negato senza permesso, scope dei token rispettati

## Notes

- Mockup: `design/mockups-v1/` schermate 01 Login, 14 Access tokens & SSH keys, 15 Organization · Teams.
