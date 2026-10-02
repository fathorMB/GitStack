---
{"area":"technical-choices","id":"DOC-bcd6d2f7-d45a-4d19-8714-66a45a9c484b","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"}],"tags":["ci","test","processo","sviluppo"],"title":"Modo di sviluppo, CI e test","updated":"2026-09-30T21:13:45.440676100+00:00"}
---

# Modo di sviluppo, CI e test

## Context

D16: sviluppo da operatore + agenti AI, con contratti espliciti e test automatici forti. Fonti nel repo: README principale, `docs/dev-environment.md`, `deploy/test-vm/README.md`, `.galaxylab/checks.toml`.

## Confirmed decisions

- **Tracciamento del lavoro:** gli item di sviluppo vivono nel tracker GalaxyLab (ID `GIT-<n>`, decisioni citate come `[c_...]`, ruoli come CTO e Atlas; il "board" è l'operatore). I commit di integrazione hanno forma `Integra GIT-<n>: ...`. Prisma non gestisce questi item: registra obiettivi e conoscenza.
- **CI GitHub Actions:** Go (build, golangci-lint, test con `-race`, test d'integrazione con Postgres reale via testcontainers e NATS in-process), TypeScript (lint, typecheck, test), shellcheck, installazione di prova del chart su k3d, test OIDC con un Keycloak reale, pubblicazione immagini su ghcr.io. Workflow separato per il contratto API. GalaxyLab esegue anche i controlli di `.galaxylab/checks.toml` a ogni integrazione.
- **Ambiente locale:** `make dev-up` / `dev-down` / `dev-redeploy SVC=<servizio>` / `dev-status` su un cluster k3d, riusando lo stesso chart e la stessa sequenza della CI; versioni pinnate.
- **Test end-to-end su VM:** script PowerShell in `deploy/test-vm/` creano e ripristinano una VM Hyper-V Ubuntu 24.04 pulita; `e2e.ps1` installa dal commit di `main` come un cliente e verifica UI, API, evento JetStream, identity, idempotenza, raccogliendo sempre la diagnostica. Lo lancia l'operatore: gli agenti non hanno accesso a Hyper-V.
- Workspace Go (`go.work`): un modulo per servizio; `scripts/go-each.sh` itera sui moduli.

## Related topics

- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/stato-di-realizzazione]]

