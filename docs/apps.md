# Applicazioni ospitate

Le applicazioni sono configurate in GitStack: ogni modifica di immagine, porta, variabili o segreti crea una nuova versione e avvia un deploy. Le variabili sono visibili a chi ha `write`; i segreti sono gestiti solo da chi ha `admin` e il loro valore non viene mai restituito dalle API.

## Segreti e rollback

Un Secret Kubernetes dell'app contiene i valori correnti dei soli nomi elencati dalla versione in esecuzione. Il Deployment lo riferisce con `envFrom`: i valori non compaiono nello spec del Deployment.

Le versioni conservano i nomi dei segreti, non i loro valori. Un redeploy o un rollback ripristina immagine, variabili e nomi dei segreti della versione scelta, ma legge sempre il valore corrente di ciascun segreto. Per questo la rotazione di un segreto resta valida anche dopo un rollback.

Se una versione nomina un segreto eliminato, GitStack rifiuta il rollback o il redeploy con `409 app_secrets_missing`, indicando i nomi mancanti. Non cambia la versione attiva e non aggiorna il Secret Kubernetes.
