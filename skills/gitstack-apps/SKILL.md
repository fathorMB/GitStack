---
name: gitstack-apps
description: Mette in produzione le app interne di un repo GitStack con gs app o con gli strumenti MCP app_*: deploy, attesa del controllo di salute, lettura dei log e rollback. Usala per pubblicare un'immagine, capire perché un deploy è fallito o tornare indietro.
---

# GitStack: deploy di un'app

Un'app è un'immagine in esecuzione dentro un repo. Con il permesso `write` sul repo un agente fa deploy, riavvio, scala e rollback da solo, con il **suo** account: ogni versione dello storico porta chi l'ha fatta (`createdBy`, tipo `agent`). Le operazioni da `admin` (creare l'app, variabili, segreti, eliminare) il tuo amministratore le concede, o no, con i grant dell'agente: se ricevi 403 (exit 5) non è un errore da aggirare, chiedi a una persona.

Il repo si indica con `--repo owner/repo` (o `-R`), oppure è quello del remote `origin`.

## Il giro: deploy → salute → log → rollback

1. **Deploy e attesa.** `--wait` aspetta il controllo di salute ed esce 0 solo se la nuova versione serve il traffico; se fallisce esce 1 (H12: resta la versione precedente).

```sh
gs app deploy web --repo acme/handbook --image sha-91be20d --wait
```

L'immagine è un tag del registry del repo (`sha-91be20d`), `nome:tag`, un digest o un riferimento esterno completo. Esiti:

- `✓ v45 healthy · 2/2 copies`: finito, passa al punto 4 solo se serve altro.
- `✗ health check failed for 2m · rolled back to v42`: il deploy è fallito e l'app serve ancora la versione precedente. Vai al punto 2.
- `deploy_in_progress` (409): un altro deploy è in corso, aspetta e riprova.
- `image_not_allowed` / `follow_branch_unsigned` (422): l'istanza accetta solo immagini firmate dalla CI del repo.

2. **Salute.** Stato, versione attiva, copie pronte e ultimo deploy con il motivo:

```sh
gs app status web --repo acme/handbook
gs app list --repo acme/handbook --json name,status,version,verified
```

3. **Log.** Leggi le righe di tutte le copie, mescolate agli eventi della piattaforma, cercando la causa:

```sh
gs app logs web --since 5m --grep panic
gs app logs web --copy web-3 --follow
```

I log servono `write`; i segreti compaiono come `***`. Se la causa è una variabile mancante e hai il permesso, correggila e rifai il deploy:

```sh
gs app env set web SEARCH_INDEX=handbook-v2
gs app deploy web --image sha-91be20d --wait
```

4. **Rollback.** Se la versione attiva è sbagliata torna a una precedente (diventa una nuova versione, con le variabili di quella versione e i valori correnti dei segreti):

```sh
gs app rollback web --to 42 --wait
gs app rollback web --to 42 --pause-auto-deploy
```

`--pause-auto-deploy` sospende i deploy automatici dal branch seguito, altrimenti la prossima immagine della CI sostituisce il ritorno.

## Altri comandi

```sh
gs app create web --image sha-91be20d --port 8080 --health-path /healthz --copies 2
gs app restart web
gs app scale web --copies 3
gs app env list web
gs app secret list web
printf %s "$VALORE" | gs app secret set web DB_PASSWORD
```

- `scale` è entro la quota dell'owner: con `insufficient_capacity` (409) il messaggio dice quanto manca. Non si scala in automatico.
- I segreti non si rileggono: `secret list` mostra solo i nomi. Passa il valore dallo standard input, mai in un argomento che resta nella cronologia.
- Con `--json` ogni comando stampa JSON e gli errori escono su stderr in JSON. Codici di uscita: 0 ok, 1 errore o deploy fallito, 2 uso errato, 4 non autenticato, 5 permesso negato, 6 non trovato.

## Con MCP

Gli strumenti sono gli stessi dell'API: `app_list`, `app_get`, `app_logs`, `app_deploy`, `app_rollback`, `app_restart`, `app_scale`, `app_version_list` (lettura o `write`), mentre `app_create`, `app_delete`, `app_set_access`, `app_access_member_add` e `app_access_member_remove` richiedono `admin` e sono annotati come **distruttivi**: il client può chiedere conferma. Il server non chiede conferme da solo. `app_deploy` risponde 202: leggi l'esito con `app_version_list` o `app_get` finché lo stato non è `serving` o `failed`.

## Divieto di `--yes` (G8)

`gs app scale --copies 0`, `gs app env unset` e `gs app secret rm` chiedono conferma. **Un agente non usa mai `--yes` su queste operazioni senza un'istruzione esplicita di una persona** che nomina quell'app, in questa conversazione. Senza terminale e senza `--yes` il comando esce con 2 e non fa nulla: è la richiesta di una conferma umana, non un errore da aggirare.

Per avere una persona nel giro dei deploy, chi amministra il repo attiva «Richiedi una Pull Request» su `main`: i deploy automatici partono solo dopo un merge approvato. Se un deploy dell'agente fallisce, gli amministratori ricevono una notifica.
