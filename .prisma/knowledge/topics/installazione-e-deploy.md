---
{"area":"requirements","id":"DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","related":["DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6"],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/milestones/M-08.md","source_id":"SRC-78fe8c86-bc59-436a-adbd-4a78dcebaab7"},{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["installer","helm","k3s","upgrade","backup"],"title":"Installazione, deploy e operazioni","updated":"2026-10-05T09:10:00+00:00"}
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

**Requisiti ancora da realizzare (M-08):** installer definitivo per Ubuntu, Debian e RHEL; Windows via WSL2 (avvio automatico, rete, riavvio); TLS con CA interna, Let's Encrypt o certificato del cliente (**oggi HTTP in chiaro**); `gitstack upgrade` con backup e rollback (D18); `gitstack backup/restore` coerente e backup giornaliero locale o S3 (D19); aggiornare k3s di un'installazione esistente; air-gapped; test CI di installazione, upgrade e ripristino; documentazione operativa.

**Immagini e rilasci:** pubblicate su ghcr.io dalla CI su `main` e sui tag; il tag di versione (`vX.Y.Z` e `X.Y.Z`) esisterà dal primo rilascio. Nessun rilascio ancora tagliato.

## Open questions

- Requisiti minimi definitivi e distribuzioni supportate: soglie provvisorie da rivedere in M-08.
- WSL2 in produzione: rischio noto (D14), vedi il topic di analisi.

## Related topics

- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/architettura]]

