# Applicazioni ospitate

Le applicazioni sono configurate in GitStack: ogni modifica di immagine, porta, variabili o segreti crea una nuova versione e avvia un deploy. Le variabili sono visibili a chi ha `write`; i segreti sono gestiti solo da chi ha `admin` e il loro valore non viene mai restituito dalle API.

## Deploy automatico da branch e firme (H7, H10)

**Firma.** La CI firma con cosign, con la chiave dell'istanza, ogni immagine che pubblica con il token del job; la chiave pubblica sta in `GET /registry/signing-key` e, per l'amministratore, in cima a `GET /admin/apps/trusted-keys`. Una versione dell'app porta `image.verified` (e `image.signedBy`): vero solo se la firma è valida. La firma della CI vale solo per **questo** repo e per **questa** immagine: la firma ricopiata da un altro repo o da un'altra immagine non conta (il riferimento firmato deve coincidere con `owner/repo[/immagine]`; se il repo viene rinominato le immagini già pubblicate risultano non verificate finché la CI non le ripubblica).

**Seguire un branch.** Con `followBranch` l'app segue un branch: quando un job della CI del repo, lanciato da un push (o da `workflow_dispatch`) su quel branch, pubblica un tag della stessa immagine dell'app, GitStack mette in produzione la nuova versione (`trigger: branch_push`, `branch`, `commitSha`). Il deploy usa i permessi di chi ha fatto il push (CI4): se non ha più `write` sul repo non succede niente. Se l'avvio fallisce resta la versione precedente (H12). Non scatta, e il perché va nel log di core:

- per un'immagine non firmata, firmata da un altro repo o da una chiave fidata aggiuntiva: il deploy automatico accetta **solo** la firma della CI di questo stesso repo, e `followBranch` su un'app con un'immagine così è rifiutato (422 `follow_branch_unsigned`);
- se l'app ha il deploy automatico sospeso (`autoDeployPaused`), per esempio dopo un rollback;
- se l'immagine è già quella dell'ultima versione (altro tag dello stesso build) o se c'è un altro deploy in corso: il prossimo push riprova.

**A mano.** Il deploy a mano (UI, API, `gs app deploy`) accetta anche immagini non firmate o esterne, etichettate «non verificata». Anche un workflow lo fa con il token del job (`Authorization: Bearer $GITSTACK_TOKEN` del job) su `POST /repos/{owner}/{repo}/apps/{app}/deploys`: serve `permissions: packages: write`, vale solo per le app del proprio repo ed è solo il deploy, nient'altro; il deploy è a nome di chi ha causato il run, che deve avere ancora `write` (un run senza autore, come una schedule, non può).

**Solo immagini firmate.** L'amministratore può accendere `signedImagesOnly` (`PUT /admin/apps/settings`): da quel momento creazione, deploy a mano e rollback accettano solo immagini firmate dalla CI del repo o da una **chiave fidata aggiuntiva**, altrimenti 422 `image_not_allowed` (un'immagine esterna non si può verificare, quindi è rifiutata). Le chiavi aggiuntive sono chiavi pubbliche cosign ECDSA in PEM (`POST /admin/apps/trusted-keys`; 422 `invalid_key`, 409 `key_exists`); si tolgono con `DELETE` (la chiave della CI dell'istanza no: 409 `instance_key`). Le app già in esecuzione non si fermano; i deploy successivi usano le chiavi di quel momento.

## Segreti e rollback

Un Secret Kubernetes dell'app contiene i valori correnti dei soli nomi elencati dalla versione in esecuzione. Il Deployment lo riferisce con `envFrom`: i valori non compaiono nello spec del Deployment.

Le versioni conservano i nomi dei segreti, non i loro valori. Un redeploy o un rollback ripristina immagine, variabili e nomi dei segreti della versione scelta, ma legge sempre il valore corrente di ciascun segreto. Per questo la rotazione di un segreto resta valida anche dopo un rollback.

Se una versione nomina un segreto eliminato, GitStack rifiuta il rollback o il redeploy con `409 app_secrets_missing`, indicando i nomi mancanti. Non cambia la versione attiva e non aggiorna il Secret Kubernetes.

## Raggiungere un'altra app del repo per nome (H15)

Dentro lo stesso repo ogni app si raggiunge con il proprio nome, come indirizzo interno del cluster: dal sito, `http://api` arriva all'app `api` dello stesso repo; un'app `postgres` si raggiunge come `postgres:5432`. Non serve configurare niente: il nome è quello che si è dato all'app.

- **Porte.** Ogni app con una porta espone due porte sul proprio indirizzo interno: la **80** (la usano anche l'indirizzo automatico e il login) e la **porta dichiarata** nella configurazione dell'app, se è diversa da 80. Quindi con porta 8080 funzionano `http://api` (porta 80, la stessa app) e `http://api:8080`; un client Postgres con porta 5432 usa `postgres:5432`. Un'app senza porta dichiarata non ha né indirizzo interno né indirizzo automatico.
- **Stabile.** Il nome resta valido dopo deploy, rollback, riavvio e scala: non dipende dai pod. Si aggiorna entro qualche secondo quando un'app del repo viene creata, rinominata o eliminata.
- **Solo lo stesso repo.** Un'app di un altro repo con lo stesso nome non si risolve e non è raggiungibile: la rete lo vieta (H9). Il traffico verso internet e verso i servizi di GitStack resta chiuso come prima; per risolvere i nomi le app possono interrogare solo il DNS del cluster.
- **Nomi non pubblicati.** Un'app non raggiunge sé stessa per nome. Non ottengono un nome interno le app chiamate `app` o `kubernetes`, o con un nome che non inizia con una lettera: il motivo compare nel log dell'app che avrebbe dovuto usarlo (evento della piattaforma). Se in un namespace esiste già un Service con quel nome, non viene sovrascritto.
- **Per chi scrive l'app.** Usare il nome come host (`http://api:8080/…`): niente indirizzi IP né l'indirizzo `*.apps.<dominio>`, che passa dal login e dal TLS. Il nome non è un permesso: l'autenticazione tra app, se serve, la fa l'app.

Meccanismo e alternative scartate: ADR 0002, sezione «Nomi interni fra app dello stesso repo (H15)».

## Variabili di sistema GITSTACK_

A ogni deploy GitStack passa al container alcune variabili di sistema, che l'app può leggere per sapere che cosa sta girando:

| Variabile | Valore |
|---|---|
| `GITSTACK_APP_VERSION` | il tag dell'immagine, per esempio `sha-1a2b3c` o `v2`. Assente se l'immagine è indicata solo per digest, senza tag. |
| `GITSTACK_DEPLOYED_AT` | data e ora della versione, in RFC 3339 UTC, per esempio `2026-10-26T07:30:15Z`. Resta la stessa a ogni riavvio della versione. |
| `GITSTACK_COMMIT_SHA` | il commit dell'immagine, dall'etichetta OCI `org.opencontainers.image.revision` (se c'è). Viene letta dal registry del repo al deploy e salvata nella versione, quindi resta la stessa a ogni riavvio; una modifica di configurazione la eredita dalla versione attiva. **Assente** se l'immagine è esterna, se non ha l'etichetta o se l'etichetta non è un commit esadecimale (da 7 a 64 caratteri). |
| `PORT` | la porta dell'app, come prima. |

Il commit di ogni versione è leggibile nell'API delle versioni dell'app, nel campo `commitSha` (`null` se la versione non ne ha uno).

Il prefisso `GITSTACK_` è riservato: non si può creare una variabile o un segreto dell'app con quel prefisso (anche in minuscolo). L'API risponde 422 con l'errore sul campo `name`, e la pagina **Variables & secrets** mostra lo stesso messaggio prima di inviare. Un nome come `MY_GITSTACK_X` è ammesso, perché il prefisso conta solo all'inizio.

## Volumi persistenti (H3)

Un'app può avere fino a 8 volumi di dimensione fissa, ognuno con un nome e un percorso di mount
assoluto (non la radice e non sotto `/proc`, `/sys`, `/dev`). Li gestisce chi ha `admin` sul repo:

- `GET /repos/{owner}/{repo}/apps/{app}/volumes` elenca i volumi e la quota disco dell'owner;
- `POST …/volumes` con `{"name", "mountPath", "sizeBytes"}` aggiunge un volume (almeno 1 MiB);
- `DELETE …/volumes/{volume}` lo elimina **insieme ai dati**: non si annulla.

Sono PVC `ReadWriteOnce` su `local-path`, lo storage di k3s. `local-path` non applica la dimensione come
limite fisico del disco: la dimensione fissa è quella che conta nella quota disco dell'owner
(`/admin/apps/quotas`, 20 GiB di default). Oltre la quota la richiesta è rifiutata (409
`insufficient_capacity`, `details.missing.diskBytes`); dall'80% la risposta porta `quota.warning: true`.

Un'app con volumi gira in **una sola copia**: la scala oltre 1 risponde 409 `single_copy`, come
l'aggiunta di un volume a un'app che ha più copie (prima si scende a una). L'aggiornamento è di tipo
`Recreate`: il pod vecchio si ferma prima che parta il nuovo, quindi c'è una breve interruzione a ogni
deploy, riavvio o modifica. Ogni aggiunta o rimozione di un volume crea una nuova versione.

I volumi sono dati, non configurazione: un rollback ripristina immagine, variabili e nomi dei segreti ma non
toglie né rimette volumi. GitStack non fa il dump di un database: per questo resta la procedura di chi lo gestisce.
