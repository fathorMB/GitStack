---
{"area":"technical-choices","id":"DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","related":["DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","TOP-4a11694d-e261-4934-bafb-f17ccc6d1726","TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5"],"reopen_reason":"Scelte P2 e P3 confermate dall'operatore il 2026-09-30: nessun accesso anonimo, visibilità privato/interno.","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["decisioni","architettura","licenze"],"title":"Decisioni di prodotto e architettura (D1–D20)","updated":"2026-10-05T13:00:00+00:00"}
---


# Decisioni di prodotto e architettura (D1–D20)

## Context

Registro delle scelte prese nell'analisi pre-sviluppo (2026-09-27) con l'operatore, una domanda alla volta. Tutte le decisioni sotto sono **accettate**; nel codice sono citate con ID GalaxyLab (es. D13 [c_c0aa0b2a2659ff40]). Origine: `.lmbrain-lite/knowledge/decisions.md` e `.lmbrain-lite/LOG.md`.

## Confirmed decisions

| ID | Tema | Decisione | Perché |
|----|------|-----------|--------|
| D1 | Scala e target | Prodotto distribuito a terzi, installato on-prem | Installer, aggiornamenti e documentazione sono requisiti dal giorno uno |
| D2 | Piattaforma | k3s incluso nell'installer | Un comando; da 1 a N nodi senza riscritture; base per ospitare app in container |
| D3 | Linguaggio backend | Go | Nativo dell'ecosistema Kubernetes; container leggeri; librerie Git mature; precedenti Gitea/Forgejo |
| D4 | Granularità servizi | Pochi servizi "grossi" (3–5): gateway, identity, git, core, web, più bus eventi | Confini chiari senza il costo dei micro-servizi fini; si spezzano quando serve |
| D5 | Identità e login | Servizio identity nostro in Go: utenti locali, chiavi SSH, token personali con scope; login esterno OIDC (Entra ID, Google, Keycloak); LDAP più avanti | Pronto all'uso, leggero, token con permessi limitati per gli agenti |
| D6 | Dati | PostgreSQL incluso (opzione DB esterno del cliente), uno schema per servizio; repo Git su volume persistente k3s | Standard, robusto, ricerca testuale, backup gestiti da GitStack |
| D7 | Web UI e API-first | React + TypeScript (SPA); la UI usa solo l'API pubblica | Tutto ciò che si fa da browser è possibile a un agente |
| D8 | Integrazione agenti | REST + OpenAPI come fonte unica; v1: CLI `gs` in Go (stile `gh`, output JSON) + skills; server MCP in v1.1, generato dalla stessa API | Funziona con qualsiasi agente con terminale |
| D9 | Codice v1 | Push/pull HTTPS (token) + SSH; browser di file, branch, tag, commit, diff, README; organizzazioni, team, ruoli, visibilità repo (**privato/interno, nessun accesso anonimo**: vedi P2 e P3 nel topic di analisi sui permessi). PR in v1.1, modello dati predisposto | Minimo per un mini GitHub; le PR sono il flusso chiave con gli agenti |
| D10 | Issues v1 | Titolo, Markdown, commenti, stato, assegnatari, ricerca + etichette e milestone + collegamenti ai commit (`#12`, `fixes #12`) + notifiche in-app/email e webhook. Kanban dopo la v1. Regole di dettaglio I1–I11 in [[knowledge/topics/issues]]; collegamenti, notifiche e webhook C1–C9 in [[knowledge/topics/collegamenti-notifiche-webhook]] | Lega codice e issues; utile agli agenti |
| D11 | Bus eventi | NATS con JetStream | Leggero, nativo Go, persistente; spina dorsale per build e deploy |
| D12 | Distribuzione | Tutto open source; ricavi da supporto e servizi | Diffusione e fiducia per un prodotto on-prem |
| D13 | Licenza server | AGPL-3.0 | Protegge dal "prendi e chiudi"; precedenti Forgejo, Grafana, Mattermost |
| D14 | Installazione | Linux + supporto ufficiale Windows via WSL2. **Rischio noto**: WSL2 in produzione è fragile (rete, avvio automatico, IP che cambiano), va testato come piattaforma a sé | Allarga il pubblico ai clienti con soli server Windows |
| D15 | Ponte verso il cloud | v1 = solo fondamenta: niente CI/registry/deploy, ma eventi di push su NATS, permessi e API su "risorse" generiche | Rilascio più veloce senza debito sul futuro |
| D16 | Team e ritmo | Operatore + agenti AI; milestone piccole (1–2 settimane) con risultato provabile; prima lo "scheletro che cammina" | Gli agenti rendono con contratti chiari e test forti |
| D17 | Licenza client | Apache-2.0 per CLI `gs`, skills e client generati; AGPL-3.0 per server e contratto OpenAPI | Nessuna barriera all'integrazione negli strumenti degli agenti |
| D18 | Aggiornamenti | `gitstack upgrade`: scarica, backup, migra il DB, aggiorna i servizi, rollback se fallisce; pulsante in UI più avanti, stesso meccanismo | Un aggiornamento fallito è un cliente perso |
| D19 | Backup e ripristino | `gitstack backup` / `restore`: archivio unico coerente (DB + repo + configurazione); backup giornaliero con conservazione ultimi N, locale o S3; ripristino testato in CI a ogni rilascio | DB e repo devono combaciare |
| D20 | Stile visivo | Layout "console" con palette **Aurora** (vedi [[knowledge/topics/design-system]]) | Pronto per le sezioni future; distinto da LMBrain |

Scelte di default (prese senza domanda, modificabili): monorepo unico; Git lato server con il binario `git` ufficiale (upload-pack/receive-pack), `go-git` solo per letture leggere; ingresso Traefik (incluso in k3s); TLS con CA interna auto-generata, opzione Let's Encrypt o certificato del cliente (HTTPS sempre, HTTP solo con `--insecure-http`: N5 in [[knowledge/topics/installazione-e-deploy]]); server SSH integrato nel servizio git su porta dedicata (**2222 di default, configurabile all'installazione**; l'installer non tocca l'SSH dell'host: R7 in [[knowledge/topics/repository-git]]); UI in inglese con i18n (italiano incluso) dalla v1; codice su GitHub finché GitStack non si ospita da solo.

## Superseded

- D20, prima proposta: palette teal, **scartata** dall'operatore perché troppo simile a LMBrain; sostituita da Aurora.
- D13, valutazione aperta di una licenza permissiva per CLI e skills: **chiusa** da D17.
- Visibilità "pubblico" dei repo (prevista in M-03 T-02 della roadmap Lite): **eliminata** il 2026-09-30 (P2, P3): nessun accesso anonimo; restano privato e interno.

## Open questions

Nessuna nella fonte originale. Punti tecnici aperti emersi dall'implementazione sono nei topic collegati.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/architettura]]
- [[knowledge/topics/identita-e-sicurezza]]

