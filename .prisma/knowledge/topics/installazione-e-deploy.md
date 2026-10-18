---
{"area":"requirements","id":"DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","related":["DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-84811cef-6f8e-4028-97d6-d47381172907","TOP-9b160889-2953-4eaf-b316-1fb22dc8f061","TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","TOP-de29e79d-90cb-4163-a1fe-2978bed63f8d","DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363"],"reopen_reason":"Requisiti dell'installer e dei backup dalla v3 (H2, H3, H4, H9; scelte dell'operatore del 2026-10-18).","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/milestones/M-08.md","source_id":"SRC-78fe8c86-bc59-436a-adbd-4a78dcebaab7"},{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["installer","helm","k3s","upgrade","backup"],"title":"Installazione, deploy e operazioni","updated":"2026-10-18T16:56:23.090925500+00:00"}
---




# Installazione, deploy e operazioni

## Context

Requisiti D1, D2, D6, D14, D18, D19 e stato dell'installer. Fonti nel repo: `deploy/README.md`, `deploy/gitstack/README.md`, `docs/operations.md`, `docs/tls.md`, `docs/rules-coverage.md`.

## Confirmed decisions

**Chart Helm `deploy/gitstack`** (un solo `helm install`): gateway, identity, git, core, web, Postgres (StatefulSet) e NATS JetStream, Ingress Traefik. Segreti generati una sola volta e conservati agli upgrade. Opzioni: Postgres esterno del cliente, registry/mirror configurabile per componente, `web.enabled`, OIDC.

**Installer (`deploy/install.sh`), stato al 2026-10-18 (commit `4a946f2`)** — un comando installa k3s e GitStack su una macchina Linux pulita (anche via `curl | sudo bash`):
- Il preflight riconosce Ubuntu 22.04/24.04 e Pop!_OS 22.04/24.04 e rifiuta gli altri sistemi salvo `--force`; installazione, backup, aggiornamento e ripristino girano in CI su Ubuntu 22.04 e 24.04.
- Preflight su hardware (N1), porte 80/443/2222, nome host (N6); HTTPS (N5); opzioni S3 (N7), Postgres esterno (N8), proxy, mirror e CA aziendale (N9); verifica delle firme (V5).
- Versioni pinnate (k3s con Traefik v3, Helm con checksum); immagini mai `latest`; idempotente.
- Installa il comando di amministrazione `gitstack` (checksum SHA-256 verificato), scrive `/etc/gitstack/config.yaml` e attiva il timer di backup giornaliero.

**Comando `gitstack` (D18, D19, realizzati):** `status`, `version`, `config set host`; `backup` coerente (breve finestra di sola lettura; database, repo, allegati, Secret e configurazione; cifratura AES-256-GCM facoltativa; conservazione degli ultimi N); `restore` sulla stessa versione; `upgrade` con prova a secco, backup preventivo, migrazioni, controllo di salute e rollback automatico (anche del database), aggiornamento di k3s (N10). Guida in `docs/operations.md`.

**Porta SSH di Git (R7, 2026-10-05):** porta **2222** di default, configurabile; l'installer non modifica mai l'SSH dell'host. Vedi [[knowledge/topics/repository-git]].

**Email e webhook (C5, C8, 2026-10-05):** SMTP facoltativo; liste di destinazioni ammesse e vietate per webhook e mirror; loopback, cluster e link-local sempre bloccati. Vedi [[knowledge/topics/collegamenti-notifiche-webhook]].

**Download di `gs` e skills (G6, 2026-10-05):** ogni istanza serve su `https://<host>/downloads` i binari di `gs` e le skills della propria versione. Vedi [[knowledge/topics/cli-gs-skills]].

**Requisiti dell'installer definitivo (N1–N6, confermati il 2026-10-05; N2 rivista il 2026-10-18):**
- **N1 Hardware:** profilo *minimo* 4 vCPU, 8 GB RAM, 60 GB di disco con 20 GB liberi (sotto, il preflight blocca); profilo *consigliato* 8 vCPU, 16 GB RAM, 200 GB SSD fino a ~100 utenti (sotto, avviso). Capacità **indicative, non misurate** (V3).
- **N2 Sistemi (rivista il 2026-10-18):** nella v1.0 sono **supportati ufficialmente solo Ubuntu Server 22.04 e 24.04 LTS**, solo amd64, ciascuno con test in CI di installazione, aggiornamento e ripristino. **Pop!_OS 22.04 e 24.04** si installa **senza `--force` ma con un avviso** chiaro ("Pop!_OS non è supportato ufficialmente nella v1.0"), senza garanzia e senza test in CI; torna candidato al supporto ufficiale dopo la v1.0. Altri sistemi solo con `--force`. Debian, RHEL e arm64 dopo la v1.
- **N3 Nodi:** un solo nodo; protezione dai guasti con backup e ripristino (obiettivo di ripristino su macchina nuova da fissare, indicativamente meno di un'ora); alta disponibilità dopo la v1.
- **N4 Rete:** internet oppure mirror o proxy aziendale configurabile (dettaglio in N9); air-gapped dopo la v1.
- **N5 TLS:** HTTPS sempre; CA interna di default con certificato della CA su `/downloads`; `--tls letsencrypt` o `--tls-cert`/`--tls-key`; HTTP solo con `--insecure-http` e avviso nella UI.
- **N6 Nome host:** `--host` con verifica DNS nel preflight; senza, nome della macchina e IP nel certificato; cambio con `gitstack config set host`.

**Regole aggiunte il 2026-10-12 (resto di M-08):**
- **N7 Backup su S3 (completa D19):** copia su storage compatibile S3 (AWS, MinIO, Backblaze B2, Wasabi, NAS con API S3) **in aggiunta** a quella locale. Configurazione all'installazione o con `gitstack config set backup.s3 …` (endpoint, bucket, prefisso, chiavi in `/etc/gitstack`, solo root). Il backup giornaliero crea l'archivio in locale e poi lo carica su S3; conservazione locale (ultimi N) e su S3 separate (default 30, oppure regole del bucket). **Cifratura obbligatoria** per gli archivi caricati su S3 (stessa chiave di `--key-file`). `gitstack restore --from s3` scarica e ripristina, anche su macchina nuova (N3). Se il caricamento fallisce, `gitstack status` lo segnala e l'amministratore riceve una notifica; il backup locale resta valido. La stessa configurazione S3 servirà a CI e registry nella v2 (CI5, RG2).
- **N8 Postgres esterno, supporto pieno:** installazione con `--postgres-url` (TLS e CA del database facoltativa); il preflight controlla versione supportata (**PostgreSQL 15–17**), connessione e permessi. Richiesti un database dedicato e un utente che può creare schemi e ruoli (GitStack crea uno schema e un ruolo per servizio); in alternativa il cliente li prepara con uno script fornito. `gitstack backup` fa il dump degli schemi di GitStack anche dal database esterno, quindi backup, S3 (N7) e `restore` funzionano come con il Postgres interno; i backup propri del cliente sono un'aggiunta. `gitstack upgrade` applica migrazioni e rollback come oggi, compreso il ripristino degli schemi. Alta disponibilità, prestazioni e aggiornamenti di versione del server Postgres sono responsabilità del cliente; `gitstack status` mostra il database esterno e la sua versione.
- **N9 Proxy, mirror e CA aziendale:** `--http-proxy`, `--https-proxy`, `--no-proxy` applicati a tutto ciò che esce dal server (installazione di k3s e Helm, download di immagini, servizi di GitStack: webhook, mirror, OIDC, SMTP; dalla v2 i job della CI). `--registry-mirror` per **tutte** le immagini, comprese quelle di sistema di k3s, con credenziali facoltative; `--download-mirror` per i binari di k3s, Helm e `gitstack`; `--ca-bundle` per fidarsi della CA aziendale (ispezione TLS) in GitStack e k3s. Il preflight prova a raggiungere tutto attraverso proxy e mirror e indica cosa non risponde. Modificabile dopo l'installazione con `gitstack config set network …`. Restano valide la verifica delle firme cosign (V5, con l'eccezione esplicita per mirror interni non firmati) e il controllo delle destinazioni di webhook e mirror (C8) anche attraverso il proxy.
- **N10 Aggiornamento di k3s:** ogni versione di GitStack dichiara la versione di k3s con cui è provata. Se cambia solo la **patch** di k3s, `gitstack upgrade` la aggiorna in automatico dopo il backup preventivo, con una breve interruzione annunciata; un cambio di **versione minore** richiede `gitstack upgrade --k3s`, una minore alla volta, sempre con backup preventivo. Il ritorno indietro di k3s è guidato da una procedura documentata, non automatico. I test in CI di aggiornamento (N2) includono il cambio di versione di k3s. Serve a mantenere le patch di sicurezza sotto GitStack (V4).

**Dalla v3 (app ospitate, regole del 2026-10-18 in [[knowledge/topics/hosting-app]]):**
- **Dominio delle app (H2):** opzione facoltativa `--apps-domain` (es. `apps.azienda.it`, con record DNS wildcard), consigliato distinto dal dominio di GitStack; senza, un default con avviso. Le app pubbliche con Let's Encrypt richiedono la porta 80 raggiungibile da internet.
- **Backup (H3, H4, completa D19):** `gitstack backup` include i volumi delle app (escludibili per app; copia con l'app accesa) e le immagini in uso dalle app (versione attiva e precedente), così dopo `restore` le app ripartono subito con i loro dati.
- **Proxy (N9):** dalla v3 vale anche per il traffico in uscita delle app (H9).

**Windows via WSL2, di prova (W1, W2, 2026-10-05):** WSL2 con Ubuntu 22.04/24.04 preparato da uno script PowerShell; per prove, demo e piccoli team. Avvio al login; di default accesso solo dal PC (`https://localhost`, SSH 2222); con `-ShareOnNetwork` rete mirrored (Windows 11 22H2+) e porte 443/2222 aperte. In CI solo un test di installazione; nessuna garanzia di disponibilità.

**Verifica dei rilasci (V5, 2026-10-05):** `install.sh` e `gitstack upgrade` verificano le firme cosign di immagini, chart e binari e si fermano se non tornano (eccezione esplicita per mirror interni non firmati). Vedi [[knowledge/topics/rilascio-v1]].

**Cosa resta (2026-10-18):** avviso per Pop!_OS nel preflight (N2 rivista; oggi Pop!_OS è accettato come Ubuntu, senza avviso); installazione con Postgres esterno e upgrade completo con rollback provati solo a mano; script WSL2 verificati solo staticamente; N1, N3, N6 da compilare in `docs/rules-coverage.md`. Dettaglio in [[knowledge/topics/stato-di-realizzazione]].

**Immagini e rilasci:** pubblicate su ghcr.io dalla CI su `main` e sui tag; nessun rilascio ancora tagliato.

## Superseded

- N2, versione del 2026-10-05: "Ubuntu Server 22.04 e 24.04 LTS, Pop!_OS 22.04 e 24.04, ciascuno con test in CI": **sostituita** il 2026-10-18 per scelta dell'operatore (GitHub non ha un runner Pop!_OS, quindi il criterio V1 non era verificabile); Pop!_OS passa a installabile con avviso.

## Open questions

Nessuna.

## Related topics

- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/architettura]]
- [[knowledge/topics/stato-di-realizzazione]]
- [[knowledge/topics/decisioni]]
- [[knowledge/topics/hosting-app]]

