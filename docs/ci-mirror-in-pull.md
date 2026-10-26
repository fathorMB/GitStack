# CI sui repo in mirror in pull

Regola CI3, precisata il 26/10/2026 (GIT-374). Su un repo che è un mirror in pull attivo (anche in pausa o in errore, non se interrotto) i workflow sono **spenti di default**.

## Accenderla

La accende chi ha `admin` sul repo, da **Settings · Secrets & variables → Workflows on the mirror** (compare solo sui mirror in pull) oppure dall'API:

```sh
# stato (403 senza admin, 404 se il repo non si legge)
GET  /repos/{owner}/{repo}/actions/pull-mirror
# {"pullMirror":true,"runOnPullMirror":false,"enabledBy":null}
PUT  /repos/{owner}/{repo}/actions/pull-mirror   {"runOnPullMirror":true}
```

Un repo che non è un mirror in pull attivo risponde `409 not_a_pull_mirror` all'accensione; un repo archiviato `409 archived`.

## Cosa succede a ogni sincronizzazione

- Con la CI accesa, ogni sincronizzazione che porta commit o tag nuovi genera gli eventi `push` e di tag **come un push normale**, uno per ogni ref aggiornato (un branch nuovo e un tag nuovo avviano due esecuzioni per workflow).
- I ref segnati `diverged` (MP1) non entrano nell'evento e non avviano niente: GitStack tiene la propria versione.
- Con la CI spenta nessuna esecuzione parte; l'avvio manuale (`workflow_dispatch`) e gli orari (`schedule`) sono bloccati come prima.

## Chi è l'autore

L'autore delle esecuzioni è **chi ha acceso la CI** sul mirror, non chi ha creato il mirror. Il token del job segue i suoi permessi (CI4): non supera mai quelli di quella persona sul repo (`write` o `admin` → al più `write`, `read` → al più `read`).

## Se chi l'ha accesa perde `admin`

La CI sul mirror **si spegne** e la persona riceve una notifica («La CI sul mirror in pull di … è stata spenta»). I ruoli stanno in identity e core non riceve un evento di cambio ruolo, quindi la verifica avviene quando la CI sta per essere usata (sincronizzazione, avvio manuale, orario) e quando l'impostazione viene letta: `GET …/actions/pull-mirror` la dà già spenta. Per riaccenderla serve un admin.
