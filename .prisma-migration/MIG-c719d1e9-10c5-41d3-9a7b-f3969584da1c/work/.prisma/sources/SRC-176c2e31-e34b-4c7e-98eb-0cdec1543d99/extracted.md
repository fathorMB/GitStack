---
id: M-01
title: Scheletro che cammina
status: active
priority: 1
created: 2026-09-27
updated: "2026-09-27"
---
# M-01 — Scheletro che cammina

## Outcome

Un comando installa k3s e GitStack su una macchina Linux: gateway, un servizio Go, Postgres, NATS e web UI rispondono end-to-end, con test automatici in CI. Base su cui si costruisce tutto il resto.

## Tasks

- [ ] T-01 Monorepo: struttura cartelle (services/, web/, cli/, skills/, deploy/, docs/), licenze AGPL/Apache, README
- [ ] T-02 CI su GitHub Actions: build, lint e test di Go e TypeScript
- [ ] T-03 Contratto API iniziale in OpenAPI con generazione client TS e Go
- [ ] T-04 Servizio gateway Go minimale (health, routing, logging strutturato)
- [ ] T-05 Servizio core Go minimale con migrazioni Postgres versionate
- [ ] T-06 NATS JetStream: libreria eventi condivisa con schema versionato e un evento di prova
- [ ] T-07 Web UI React+TS: scheletro, layout, chiamata all'API tramite gateway
- [ ] T-08 Manifest k3s (Helm chart interno) per tutti i componenti
- [ ] T-09 Installer v0: script che installa k3s e GitStack su Linux pulito
- [ ] T-10 Ambiente di sviluppo locale riproducibile (k3d o simile) documentato
- [ ] T-11 Test end-to-end: installazione su VM pulita e verifica che UI e API rispondano
- [x] T-12 Styleguide di prodotto e mockup delle schermate v1 in design/

## Notes

- Design: stile e token in `design/styleguide/` (vedi [[design-system]]); shell dell'app (sidebar, topbar) nelle schermate di `design/mockups-v1/`.
