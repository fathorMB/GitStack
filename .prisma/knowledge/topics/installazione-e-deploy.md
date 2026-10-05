---
{"area":"requirements","id":"DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","related":["DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-84811cef-6f8e-4028-97d6-d47381172907","TOP-9b160889-2953-4eaf-b316-1fb22dc8f061","TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5"],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/milestones/M-08.md","source_id":"SRC-78fe8c86-bc59-436a-adbd-4a78dcebaab7"},{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["installer","helm","k3s","upgrade","backup"],"title":"Installazione, deploy e operazioni","updated":"2026-10-05T13:20:00+00:00"}
---

# Installazione, deploy e operazioni

## Context

Requisiti D1, D2, D6, D14, D18, D19 e stato dell'installer. Fonti nel repo: `deploy/README.md`, `deploy/gitstack/README.md`.

## Confirmed decisions

**Chart Helm `deploy/gitstack`** (un solo `helm install`): gateway, identity, core, web, Postgres (StatefulSet) e NATS JetStream, Ingress Traefik. Segreti generati una sola volta e conservati agli upgrade (password Postgres, ruoli per servizio, admin, segreto di servizio, chiave OIDC). Opzioni: Postgres esterno del cliente, registry/mirror configurabile per componente (base per l'air-gapped), `web.enabled`, OIDC. Il PVC dei repo Git c'è ma è spento finché non arriva il servizio git.

**Installer v0 (`deploy/install.sh`)** — un comando installa k3s e GitStack su una macchina Linux pulita (anche via `curl | sudo bash`):
- Supporto solo **Ubuntu Server 24.04 LTS x86_64**. Requisiti minimi provvisori: 4 vCPU, 8 GB RAM, disco ≥ 55 GB con ≥ 20 GB liberi, porte 80/443/6443 libere, uscita verso get.k3s.io, get.helm.sh e il registry. Controlli di preflight con messaggi chiari.
- Versioni pinnate (k3s con Traefik v3, Helm con checksum); immagini con tag `sha-<commit>`, mai `latest`.
- Idempotente: rieseguirlo non reinstalla k3s né cambia password.
- A fine installazione mostra gli URL della UI e dell'API; la password admin si legge dal Secret.

**Porta SSH di Git (R7, 2026-10-05):** il servizio git espone SSH sulla porta **2222** di default, configurabile all'installazione; l'installer la aggiunge ai controlli di preflight e non modifica mai l'SSH della macchina host. Vedi [[knowledge/topics/repository-git]].

**Email e webhook (C5, C8, 2026-10-05):** l'SMTP è facoltativo: senza configurazione GitStack invia solo notifiche in-app. L'amministratore può impostare liste di destinazioni ammesse e vietate per i webhook; loopback, indirizzi interni del cluster e link-local restano sempre bloccati. Vedi [[knowledge/topics/collegamenti-notifiche-webhook]].

**Download di `gs` e skills (G6, 2026-10-05):** ogni istanza serve su `https://<host>/downloads` i binari di `gs` per Linux, macOS e Windows (amd64, arm64) e le skills della propria versione, con gli script `install-gs.sh` e PowerShell; funziona anche air-gapped. Vedi [[knowledge/topics/cli-gs-skills]].

**Requisiti dell'installer definitivo (N1–N6, confermati il 2026-10-05):**
- **N1 Hardware:** profilo *minimo* 4 vCPU, 8 GB RAM, 60 GB di disco con 20 GB liberi (sotto, il preflight blocca); profilo *consigliato* 8 vCPU, 16 GB RAM, 200 GB SSD fino a ~100 utenti (sotto, avviso). Spazio per i repo: circa 2 volte la loro dimensione.
- **N2 Sistemi:** Ubuntu Server 22.04 e 24.04 LTS, Pop!_OS 22.04 e 24.04, solo amd64, ciascuno con test in CI di installazione, aggiornamento e ripristino; altri sistemi solo con `--force`. Debian, RHEL e arm64 dopo la v1.
- **N3 Nodi:** un solo nodo; protezione dai guasti con backup e ripristino (obiettivo di ripristino su macchina nuova da fissare, indicativamente meno di un'ora); alta disponibilità dopo la v1.
- **N4 Rete:** internet oppure mirror o proxy aziendale configurabile; pacchetto e aggiornamenti offline (air-gapped) dopo la v1.
- **N5 TLS:** HTTPS sempre; CA interna di default con certificato della CA su `/downloads` e installabile da `install-gs.sh`; `--tls letsencrypt` o `--tls-cert`/`--tls-key`; HTTP solo con `--insecure-http` e avviso nella UI.
- **N6 Nome host:** `--host` con verifica DNS nel preflight; senza, nome della macchina e IP nel certificato per le prove; cambio con `gitstack config set host` (rigenera il certificato, avvisa del cambio degli URL).

**Windows via WSL2, di prova (W1, W2, 2026-10-05):** WSL2 con Ubuntu 22.04/24.04 preparato da uno script PowerShell (attiva WSL2, installa Ubuntu, lancia l'installer); per prove, demo e piccoli team, mentre per server condivisi si usa Ubuntu Server anche in una VM. Avvio al login tramite attività di Windows; di default accesso solo dal PC (`https://localhost`, SSH 2222); con `-ShareOnNetwork` rete mirrored di WSL2 (Windows 11 22H2+) e porte 443/2222 aperte nel firewall. In CI solo un test di installazione; nessuna garanzia di disponibilità.

**Requisiti ancora da realizzare (M-08):** installer definitivo per Ubuntu Server e Pop!_OS (N2); Windows via WSL2 di prova (W1, W2); TLS con CA interna, Let's Encrypt o certificato del cliente (**oggi HTTP in chiaro**); `gitstack upgrade` con backup e rollback (D18); `gitstack backup/restore` coerente e backup giornaliero locale o S3 (D19); aggiornare k3s di un'installazione esistente; air-gapped; test CI di installazione, upgrade e ripristino; documentazione operativa.

**Immagini e rilasci:** pubblicate su ghcr.io dalla CI su `main` e sui tag; il tag di versione (`vX.Y.Z` e `X.Y.Z`) esisterà dal primo rilascio. Nessun rilascio ancora tagliato.

## Open questions


## Related topics

- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/architettura]]

