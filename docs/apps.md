# Applicazioni ospitate

Le applicazioni sono configurate in GitStack: ogni modifica di immagine, porta, variabili o segreti crea una nuova versione e avvia un deploy. Le variabili sono visibili a chi ha `write`; i segreti sono gestiti solo da chi ha `admin` e il loro valore non viene mai restituito dalle API.

## Segreti e rollback

Un Secret Kubernetes dell'app contiene i valori correnti dei soli nomi elencati dalla versione in esecuzione. Il Deployment lo riferisce con `envFrom`: i valori non compaiono nello spec del Deployment.

Le versioni conservano i nomi dei segreti, non i loro valori. Un redeploy o un rollback ripristina immagine, variabili e nomi dei segreti della versione scelta, ma legge sempre il valore corrente di ciascun segreto. Per questo la rotazione di un segreto resta valida anche dopo un rollback.

Se una versione nomina un segreto eliminato, GitStack rifiuta il rollback o il redeploy con `409 app_secrets_missing`, indicando i nomi mancanti. Non cambia la versione attiva e non aggiorna il Secret Kubernetes.

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
