---
{"area":"delivery","id":"DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","related":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d"],"reopen_reason":"Domanda su GL-31 chiusa dall'operatore il 2026-10-18: GL-31 coincide con la tappa 2 di V7.","schema_version":1,"sources":[{"origin_path":".lmbrain-lite/ROADMAP.md","source_id":"SRC-e44cf26d-7f3c-466b-a405-558022e98ca5"},{"origin_path":".lmbrain-lite/milestones/M-01.md","source_id":"SRC-176c2e31-e34b-4c7e-98eb-0cdec1543d99"},{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"}],"tags":["stato","milestone","v1"],"title":"Stato di realizzazione della v1","updated":"2026-10-18T00:11:04.528903+00:00"}
---


# Stato di realizzazione della v1

## Context

Stato **ricostruito dal repo** (`docs/rules-coverage.md`, `docs/operations.md`, `docs/release-checklist.md`, `docs/security/asvs-l2.md`, `docs/security/threat-model.md`, `AGENTS.md`), riallineato il 2026-10-18 al commit `4a946f2` su `main` e confermato dall'operatore. Allineamenti precedenti: `af42239` (2026-09-30), `de88a0e` e `1587133` (2026-10-05), `e708707` (2026-10-12). "Completata" significa che il codice copre tutto ciò che la milestone prevede e la sua famiglia di regole è coperta da test; la chiusura formale degli item resta nel tracker GalaxyLab. Una riga `parziale` di `rules-coverage.md` dichiara cosa manca; non è per forza codice mancante.

## Confirmed decisions

| Milestone | Stato | Prove |
|---|---|---|
| M-01 Scheletro che cammina | **completata** | GIT-1…GIT-28 |
| M-02 Identità, organizzazioni e permessi | **completata** | GIT-29…GIT-62, GIT-78, GIT-79, GIT-95; famiglia P coperta |
| M-03 Hosting Git | **completata** | GIT-65…GIT-77, GIT-99, GIT-100; parte installer di R7 passata a M-08 |
| M-04 Browser del codice | **completata** | GIT-80…GIT-94, GIT-116…GIT-121 |
| M-05 Issues | **completata** | GIT-101…GIT-113 |
| M-06 Collegamenti, notifiche, webhook | **completata** | famiglia C1–C9 coperta |
| M-07 CLI `gs` e skills | **completata** | famiglia G1–G8 coperta; guida alla prova reale di un agente (GIT-174, `docs/agent-trial.md`) |
| M-08 Installazione, upgrade, backup | **quasi completa** | codice per N1–N10, W1, W2 e V5 presente; restano prove e l'avviso per Pop!_OS (vedi sotto) |
| M-09 Rilascio v1.0 | **in corso** | mirror in pull e import (MP1–MP8), revisione di sicurezza e correzioni, fumo, lista di controllo della rc; nessun tag di versione |

### M-08: cosa c'è e cosa manca (2026-10-18)

- **Fatto:** preflight che riconosce Ubuntu 22.04/24.04 e Pop!_OS 22.04/24.04 (N2); installazione, backup, aggiornamento (anche di patch di k3s) e ripristino in CI su Ubuntu 22.04 e 24.04; backup su S3 cifrato con `restore --from s3` (N7); Postgres esterno 15–17 con preflight, backup e restore degli schemi (N8); proxy, mirror di registry e download, CA aziendale, policy di uscita applicata anche dietro proxy (N4, N9); aggiornamento di k3s in `gitstack upgrade` (N10); verifica delle firme cosign in `install.sh` e `gitstack upgrade` (V5); script PowerShell per WSL2 di prova (W1, W2); HTTPS (N5).
- **Manca:** avviso "non supportato ufficialmente nella v1.0" su Pop!_OS (N2 rivista il 2026-10-18: nella v1.0 sono supportati solo Ubuntu Server 22.04 e 24.04, quindi la CI su Ubuntu basta per il criterio V1); installazione completa con Postgres esterno e upgrade completo con rollback provati solo a mano; script WSL2 verificati solo staticamente; N1, N3 e N6 risultano "da compilare" in `rules-coverage.md` (il controllo della release li conta come mancanti).

### M-09: cosa c'è e cosa manca (2026-10-18)

- **Fatto:** mirror in pull e import una tantum (MP1–MP6, MP8 coperte); mirror in push (V8-mirror); revisione di sicurezza interna scritta (ASVS L2 e threat model, GIT-219) con 20 problemi, di cui risolti F1 e F2 (alti) e F4–F11 (medi; F8 con residuo: niente TLS dentro il cluster); test di fumo (V3); lista di controllo della rc e script `release-readiness` (V1, GIT-226); job di rilascio firmato con SBOM e changelog (V4/V5, scritto); sito di documentazione che si costruisce (V6); istruzioni di sviluppo per gli agenti dopo la tappa 2 (`AGENTS.md`).
- **Manca:** scheda Settings · Pull mirror nella UI (MP7) e «Sincronizza ora» con stato del mirror in push (GIT-180, V8); prova del job di rilascio su un tag `v0.0.0-test.N`; pubblicazione del sito (azione del board) e parti ancora in italiano; politica di supporto di V4; tappa 2 e dogfooding (V7); allineare `docs/release-checklist.md` e `scripts/release-readiness.go` alla soglia di sicurezza decisa il 2026-10-18 ([[knowledge/topics/rilascio-v1]]) e togliere Pop!_OS dal passo 6 della checklist.
- **Non bloccante per la v1.0 (scelta del 2026-10-18):** F3 (medio) e F12–F20 (bassi) aperti, da elencare nelle note di rilascio.

### Tappa 2 di V7: non ancora fatta (operatore, 2026-10-18)

L'item GalaxyLab **GL-31 ("fase 3") coincide con la tappa 2** e non chiede altro (operatore, 2026-10-18): cinque condizioni, «Interrompi» sul mirror in pull, mirror in push verso GitHub, agenti che lavorano su homehub.

Le 4 settimane di dogfooding richieste da V1 **non sono partite**. Stato delle cinque condizioni di [[knowledge/topics/rilascio-v1]]:

1. mirror in pull su homehub da almeno 2 settimane senza errori: **da verificare** (non visibile dal repo);
2. backup giornalieri e ripristino di prova su una macchina nuova: **procedura pronta, non eseguita** (`docs/operations.md`, "Prove della tappa 2 di V7");
3. prova reale di un agente con `gs` e skills su homehub: **da verificare**;
4. mirror in push provato su un repo di prova senza `diverged`: **procedura pronta, non eseguita**;
5. istruzioni di sviluppo aggiornate: **fatta** (`AGENTS.md`, scritto prima della tappa).

Il dettaglio di task e avanzamento resta nel tracker GalaxyLab, non in Prisma.

## Open questions

- `deploy/README.md` e `docs/operations.md` citano ancora `.lmbrain-lite`: da aggiornare quando M-08 chiude.

## Related topics

- [[knowledge/topics/visione]]
- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/rilascio-v1]]

