# Applicazioni ospitate

Le applicazioni sono configurate in GitStack: ogni modifica di immagine, porta, variabili o segreti crea una nuova versione e avvia un deploy. Le variabili sono visibili a chi ha `write`; i segreti sono gestiti solo da chi ha `admin` e il loro valore non viene mai restituito dalle API.

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
