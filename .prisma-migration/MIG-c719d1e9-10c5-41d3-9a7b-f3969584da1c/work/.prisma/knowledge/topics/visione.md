---
{"area":"vision","id":"DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/vision.md","source_id":"SRC-7daaa215-dddf-4cb7-8c49-4b4f2e2b77fb"},{"origin_path":".lmbrain-lite/PROJECT.md","source_id":"SRC-d874a7c7-8daf-42a2-ba1e-534a8cee4973"}],"tags":["visione","principi","percorso"],"title":"Visione di prodotto","updated":"2026-09-30T21:13:45.434740+00:00"}
---

# Visione di prodotto

## Context

GitStack parte come "mini GitHub" open source da installare on-prem con poca configurazione e cresce fino a un piccolo cloud privato ("un piccolo Azure") dove si fanno build, deploy e hosting di applicazioni in container, tutto da web UI. Utenti: team e aziende che vogliono codice e issues sui propri server, senza cloud esterni; gli **agenti di coding** sono utenti di prima classe (CLI `gs`, skills, poi server MCP).

## Confirmed decisions

Percorso a tappe (accettato nell'analisi del 2026-09-27):

1. **v1 — Codice e Issues.** Repo Git (HTTPS + SSH), browser del codice, organizzazioni e permessi, issues con etichette, milestone, collegamenti ai commit, notifiche e webhook. CLI `gs` + skills per agenti.
2. **v1.1 — Collaborazione.** Pull Request (diff, review, merge), server MCP.
3. **v2 — Build.** CI minima (stile GitHub Actions) che reagisce agli eventi di push; registry di immagini container.
4. **v3 — Hosting.** Deploy di app e container generici su k3s da web UI: domini, variabili d'ambiente, log, scalabilità.
5. **Oltre.** Servizi gestiti (database, code, storage), multi-nodo con alta affidabilità.

Principi:

- **Un comando per installare.** Il cliente non deve conoscere Kubernetes.
- **API-first.** UI, CLI, skills e MCP usano la stessa API pubblica; nessuna funzione esclusiva della UI.
- **Agenti come utenti di prima classe.** Token con permessi limitati, output JSON, skills mantenute insieme al prodotto.
- **Risorse generiche.** Permessi, eventi e API parlano di "risorse" (repo oggi; app e database domani), così le tappe successive si agganciano senza riscritture.
- **Open source.** AGPL-3.0 per il server, Apache-2.0 per i client.

Fuori ambito v1: Pull Request (v1.1, ma previste nel modello dati), bacheca Kanban, CI/build, registry container, deploy di app, server MCP, LDAP.

## Open questions

- La tappa "Oltre" è una direzione, non ancora un obiettivo di roadmap.

## Related topics

- [[knowledge/topics/decisioni]]
- [[knowledge/topics/architettura]]
- [[knowledge/topics/stato-di-realizzazione]]

