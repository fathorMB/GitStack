---
{"horizon":"now","id":"OBJ-184e52c5-3841-4a77-9cea-fad781f5a813","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"L'operatore indica come fatto l'avviso per Pop!_OS nel preflight (N2 rivista), 2026-10-19.","schema_version":1,"title":"Installazione, aggiornamenti e backup","updated":"2026-10-18T22:03:23.281163200+00:00"}
---



# Installazione, aggiornamenti e backup

## Outcome

Un cliente installa GitStack su Ubuntu Server 22.04 o 24.04 con un comando (o su Windows via WSL2 per prove), anche in una rete aziendale chiusa e con il proprio Postgres, lo aggiorna in sicurezza con rollback (k3s compreso) e fa backup e ripristino coerenti, anche su S3.

## Rationale

D1, D14, D18, D19: per un prodotto on-prem installazione, aggiornamenti e backup sono requisiti dal giorno uno; un aggiornamento fallito è un cliente perso. N10 tiene le patch di sicurezza di k3s sotto GitStack (V4). Si promette solo ciò che la CI può verificare: per questo Pop!_OS esce dai sistemi supportati della v1.0 (scelta del 2026-10-18).

## Scope

Requisiti N1–N10, W1, W2 e V5 in [[knowledge/topics/installazione-e-deploy]].

Avanzamento al commit `4a946f2` (2026-10-18), aggiornato il 2026-10-19, **quasi completa**:
- **fatto:** installer con preflight (N1, N2, N6), con l'avviso "non supportato ufficialmente nella v1.0" quando trova Pop!_OS (N2 rivista); HTTPS (N5); `gitstack` status/backup/restore/upgrade con rollback e timer giornaliero; backup su S3 cifrato con `restore --from s3` (N7); Postgres esterno 15–17 (N8); proxy, mirror di registry e download, CA aziendale (N4, N9); aggiornamento di k3s (N10); verifica delle firme cosign (V5); script WSL2 di prova (W1, W2); installazione, aggiornamento e ripristino in CI su Ubuntu 22.04 e 24.04;
- **resta:** installazione con Postgres esterno e upgrade completo con rollback provati solo a mano; WSL2 verificato solo staticamente; compilare N1, N3, N6 in `rules-coverage.md`.

Fuori dalla v1: supporto ufficiale di Pop!_OS (candidato dopo la v1.0), Debian, RHEL e derivate, arm64, più nodi, air-gapped, supporto di produzione su Windows, alta disponibilità del Postgres esterno (responsabilità del cliente).

Priorità 8 di 9 nella v1. Mockup 16.

## Source references

- `.lmbrain-lite/milestones/M-08.md`
- `deploy/README.md`, `docs/operations.md`, `docs/rules-coverage.md` (famiglie N, W, V5)
- Temi di analisi "Requisiti minimi e distribuzioni dell'installer definitivo" (N1–N6) e "Supporto Windows via WSL2 in produzione" (W1, W2)
- Scelte dell'operatore del 2026-10-12 (N7–N10) e del 2026-10-18 (N2 rivista)


