---
title: Visione di prodotto
updated: 2026-09-27
---

# Visione

GitStack parte come mini GitHub on-prem e cresce fino a un piccolo cloud privato. Decisioni in [[decisions]], struttura tecnica in [[architecture]].

## Percorso a tappe

1. **v1 — Codice e Issues.** Repo Git (HTTPS + SSH), browser del codice, organizzazioni e permessi, issues con etichette, milestone, collegamenti ai commit, notifiche e webhook. CLI `gs` + skills per agenti.
2. **v1.1 — Collaborazione.** Pull Request (diff, review, merge), server MCP.
3. **v2 — Build.** CI minima (stile GitHub Actions) che reagisce agli eventi di push; registry di immagini container.
4. **v3 — Hosting.** Deploy di app e container generici su k3s da web UI: domini, variabili d'ambiente, log, scalabilità.
5. **Oltre.** Servizi gestiti (database, code, storage), multi-nodo con alta affidabilità.

## Principi

- **Un comando per installare.** Il cliente non deve conoscere Kubernetes.
- **API-first.** UI, CLI, skills e MCP usano la stessa API pubblica.
- **Agenti come utenti di prima classe.** Token con permessi limitati, output JSON, skills mantenute insieme al prodotto.
- **Risorse generiche.** Permessi, eventi e API parlano di "risorse" (repo oggi; app e database domani), così le tappe successive si agganciano senza riscritture.
- **Open source.** AGPL-3.0.
