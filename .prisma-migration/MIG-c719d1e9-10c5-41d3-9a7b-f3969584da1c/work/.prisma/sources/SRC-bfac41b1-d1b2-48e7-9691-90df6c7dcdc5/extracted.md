---
title: Decisioni di prodotto e architettura
updated: 2026-09-27
---

# Decisioni

Registro delle scelte prese durante l'analisi pre-sviluppo. Vedi anche [[vision]].

| ID | Tema | Decisione | Perché |
|----|------|-----------|--------|
| D1 | Scala e target | Prodotto distribuito a terzi, installato on-prem | Installer, aggiornamenti e documentazione sono requisiti dal giorno uno |
| D2 | Piattaforma | k3s incluso nell'installer | Un comando per installare; da 1 a N nodi senza riscritture; base naturale per ospitare app in container (visione "piccolo Azure") |
| D3 | Linguaggio backend | Go | Nativo dell'ecosistema Kubernetes; container leggeri; librerie Git mature; precedenti (Gitea, Forgejo) |
| D4 | Granularità servizi | Pochi servizi "grossi" (3–5): gateway/identità, Git, API core, web UI, più bus eventi | Confini chiari senza il costo operativo dei micro-servizi fini; si spezzano quando serve |
| D5 | Identità e login | Servizio identità nostro in Go: utenti locali, chiavi SSH, token personali con scope; login esterno via OIDC (Entra ID, Google, Keycloak); LDAP più avanti | Pronto all'uso dopo l'installazione, leggero, token con permessi limitati per gli agenti di coding |
| D6 | Dati | PostgreSQL incluso nell'installer (opzione DB esterno del cliente) per i dati strutturati; repo Git su volume persistente k3s | Standard e robusto, ricerca testuale inclusa, backup gestiti da GitStack |
| D7 | Web UI e principio API-first | React + TypeScript (SPA); la UI usa solo l'API pubblica, la stessa di agenti, CLI e skills | Tutto ciò che si fa da browser è possibile anche a un agente; ecosistema di componenti ricco |
| D8 | Integrazione agenti | API REST + OpenAPI come base; v1: CLI `gs` in Go (stile `gh`, output JSON) + pacchetto skills; server MCP nella versione successiva, generato dalla stessa API | Funziona con qualsiasi agente che usa un terminale; stesso strumento per umani e CI |
| D9 | Codice v1 | Push/pull HTTPS (token) + SSH; browser web di file, branch, tag, commit, diff, README; organizzazioni, team, ruoli, visibilità repo. Pull Request in v1.1, ma il modello dati le prevede da subito | Minimo indispensabile per un mini GitHub venduto a team; le PR sono il flusso chiave con gli agenti |
| D10 | Issues v1 | Base (titolo, Markdown, commenti, stato, assegnatari, ricerca) + etichette e milestone + collegamenti ai commit (`#12`, `fixes #12`) + notifiche in-app/email e webhook. Bacheca Kanban dopo la v1 | Lega codice e issues; utile agli agenti per filtrare e chiudere lavoro |
| D11 | Bus eventi | NATS con JetStream | Leggero, nativo Go, messaggi persistenti; spina dorsale per il futuro (build, deploy) |
| D12 | Modello di distribuzione | Tutto open source; eventuali ricavi da supporto e servizi | Massima diffusione e fiducia per un prodotto on-prem |
| D13 | Licenza | AGPL-3.0 per il server (client: vedi D17) | Protegge dal "prendi e chiudi"; precedenti: Forgejo, Grafana, Mattermost |
| D14 | Installazione | Script per server Linux + supporto ufficiale a Windows tramite WSL2. Rischio noto: WSL2 in produzione è fragile (rete, avvio automatico, IP che cambiano) e va testato come piattaforma a sé | Allarga il pubblico ai clienti con soli server Windows |
| D15 | Ponte verso il cloud privato | v1 = solo fondamenta: niente CI/registry/deploy come funzioni, ma eventi di push su NATS, permessi e API modellati su "risorse" generiche (repo oggi, app e database domani) | Rilascio più veloce senza debito sul futuro |
| D16 | Team e ritmo | Operatore + agenti AI di coding. Milestone piccole (1–2 settimane) con risultato provabile; la prima è lo "scheletro che cammina" | Gli agenti rendono al meglio con contratti chiari (OpenAPI, schemi DB) e test automatici forti |
| D20 | Stile visivo | Layout "console" (sidebar scura fissa, densità alta, tema chiaro e scuro) con palette **Aurora**: accento viola, sfumatura viola → magenta → arancio per il marchio, issue chiuse in magenta, agenti in arancio. Dettagli in [[design-system]] | Layout pronto per le sezioni future del cloud; palette distinta da LMBrain (prima proposta teal scartata perché troppo simile) |

## Scelte di default (prese senza domanda, modificabili)

- **Monorepo** unico: servizi Go, web UI, CLI, skills, installer, documentazione.
- **Git lato server** con il binario `git` ufficiale (upload-pack/receive-pack) per push/pull; `go-git` solo per letture leggere. Più robusto e compatibile, come fa Gitea.
- **Ingresso HTTP**: Traefik (già incluso in k3s). TLS: certificato auto-generato da una CA interna all'installazione, con opzione Let's Encrypt o certificato del cliente.
- **SSH**: server SSH integrato nel servizio Git (porta dedicata), non l'SSH del sistema.
- **Lingua dell'interfaccia**: inglese di default, traduzioni (italiano incluso) dalla v1.
- **Sviluppo del prodotto stesso**: codice su GitHub finché GitStack non è in grado di ospitarsi da solo (dogfooding).

| D17 | Licenza client | Apache-2.0 per CLI `gs`, skills e client generati; AGPL-3.0 solo per il server | Nessuna barriera all'integrazione negli strumenti degli agenti; il cuore resta protetto |
| D18 | Aggiornamenti | Comando `gitstack upgrade`: scarica, fa backup, migra il DB, aggiorna i servizi, rollback se fallisce. Pulsante in UI più avanti, stesso meccanismo | Un aggiornamento fallito è un cliente perso; semplice da capire e testare |
| D19 | Backup e ripristino | `gitstack backup` / `gitstack restore`: archivio unico e coerente (DB + repo + configurazione); backup giornaliero automatico con conservazione ultimi N, destinazione locale o S3 compatibile; ripristino testato in CI a ogni rilascio | DB e repo devono combaciare al ripristino |

## Punti aperti

Nessuno al momento.
