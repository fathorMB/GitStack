---
{"depends_on":[],"id":"TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e"],"schema_version":1,"state":"consolidated","title":"Requisiti minimi e distribuzioni dell'installer definitivo","updated":"2026-10-05T13:00:00+00:00"}
---

# Requisiti minimi e distribuzioni dell'installer definitivo

## Expected learning

Fissare i requisiti hardware e le distribuzioni supportate dalla v1, oggi provvisori (4 vCPU, 8 GB RAM, 60 GB disco, solo Ubuntu 24.04).

## Già deciso (da altri temi)

- D1, D2, D6: k3s incluso, installazione con un comando, Postgres incluso o esterno, repo su volume persistente.
- R7: SSH di Git sulla porta 2222, l'installer non tocca l'SSH dell'host. C5: SMTP facoltativo. G6: l'istanza serve `gs` e skills su `/downloads`, anche air-gapped.
- Installer v0: solo Ubuntu Server 24.04 x86_64, soglie provvisorie 4 vCPU, 8 GB RAM, disco ≥ 55 GB (≥ 20 GB liberi), porte 80/443/6443, preflight con messaggi chiari.

## Scelte confermate dall'operatore (2026-10-05)

- **N1 — Due profili hardware:** *minimo* (prove e team fino a ~20 persone): 4 vCPU, 8 GB RAM, 60 GB di disco con almeno 20 GB liberi; sotto queste soglie il preflight **blocca**. *Consigliato* (uso reale fino a ~100 utenti tra persone e agenti): 8 vCPU, 16 GB RAM, 200 GB SSD; sotto queste soglie il preflight **avvisa** e lascia procedere. Spazio per i repo stimato a parte: circa 2 volte la dimensione dei repo (cestino R2 e backup locali D19).
- **N2 — Sistemi supportati nella v1: Ubuntu e Pop!_OS:** Ubuntu Server 22.04 e 24.04 LTS, Pop!_OS 22.04 e 24.04, solo amd64. Ogni combinazione ha test automatici di installazione, aggiornamento e ripristino in CI. Su altri sistemi il preflight avvisa "non supportato" e procede solo con `--force`. Pop!_OS è indicato per sviluppo, demo e piccoli team; per i server dei clienti il riferimento è Ubuntu Server. **Cambio di scope:** Debian e RHEL (con derivate) escono dalla v1, scelta esplicita dell'operatore in conflitto con lo scope precedente di M-08; restano candidati dopo la v1, come arm64.
- **N3 — Un solo nodo nella v1:** l'installer mette tutto su una macchina (k3s, servizi, Postgres, NATS, repo). La protezione dai guasti viene da backup e ripristino (D19) con un obiettivo di ripristino dichiarato su una macchina nuova (indicativamente meno di un'ora, da fissare in M-08). Postgres esterno resta possibile (D6). Alta disponibilità su più nodi dopo la v1, verso il "piccolo cloud" (v3).
- **N4 — Air-gapped dopo la v1, mirror e proxy nella v1:** nella v1 l'installazione richiede internet oppure un mirror o proxy aziendale configurabile per registry e download. Il pacchetto offline unico (k3s, immagini, chart) e gli aggiornamenti offline arrivano dopo la v1; le basi già presenti (versioni bloccate, registry configurabile per componente, `gs` servito dall'istanza G6) non vanno compromesse.
- **N5 — HTTPS sempre, CA interna di default:** senza opzioni l'installer crea una CA interna e un certificato per il nome host; GitStack risponde subito in HTTPS. Il certificato della CA è scaricabile da `/downloads` (G6) e `install-gs.sh` può aggiungerlo ai certificati fidati della macchina, così `gs` e `git` funzionano senza errori. Alternative: `--tls letsencrypt` (nome raggiungibile da internet) o `--tls-cert`/`--tls-key` (certificato del cliente). HTTP in chiaro solo con `--insecure-http`, per prove locali, con avviso visibile nella UI.
- **N6 — Nome host consigliato, alternative per le prove:** l'installer accetta `--host <nome>`; il preflight verifica che il nome risolva alla macchina e avvisa se no. Senza `--host` usa il nome completo della macchina; il certificato include anche l'indirizzo IP per prove veloci. Il nome si cambia dopo con `gitstack config set host`, che rigenera il certificato e avvisa che URL di clone e indirizzi nelle email cambiano.

## Open questions

1. ~~Requisiti hardware~~ (N1).
2. ~~Sistemi supportati~~ (N2).
3. ~~Nodi~~ (N3).
4. ~~Air-gapped~~ (N4, dopo la v1).
5. ~~TLS~~ (N5).
6. ~~Nome host e DNS~~ (N6).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole N1–N6: due profili hardware (minimo bloccante 4 vCPU/8 GB/60 GB, consigliato con avviso 8 vCPU/16 GB/200 GB SSD); sistemi supportati Ubuntu Server e Pop!_OS 22.04/24.04 amd64 con test in CI (Debian e RHEL escono dalla v1); un solo nodo con ripristino da backup; mirror e proxy nella v1, air-gapped dopo; HTTPS sempre con CA interna di default, Let's Encrypt o certificato del cliente, HTTP solo con `--insecure-http`; `--host` consigliato con verifica DNS e alternative per le prove. Riportato in [[knowledge/topics/installazione-e-deploy]], nello scope di M-08 e nelle scelte di default di [[knowledge/topics/decisioni]]. Mockup 16 da aggiornare.
