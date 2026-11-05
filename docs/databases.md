# Database gestiti (PostgreSQL)

Guida per chi **usa** i database gestiti di GitStack (v3-d, [ADR 0003](adr/0003-database-gestiti.md)):
creare un database, collegarlo a un'app, fare e ripristinare i backup, collegarsi dal proprio PC con
pgAdmin o DBeaver, e passare da un Postgres ospitato come app con volume a un database gestito.
Chi amministra l'host (accensione, porte, firewall, upgrade) trova il resto in
[`operations.md`](operations.md): «Managed databases», «Database gestiti: accesso dalla LAN e firewall»
e «Managed databases: backup and restore».

## Cos'è e cosa serve

Un database gestito è un PostgreSQL **16 o 17** che GitStack avvia in un proprio namespace
(`gitstack-db-<id>`), con volume, TLS e backup. È di un **owner** (utente o organizzazione), come un
repo, e conta nella stessa quota di CPU, RAM e disco delle app dell'owner (oltre il tetto:
`409 insufficient_capacity`).

* **Funzione spenta di default.** Se l'amministratore non l'ha accesa, ogni operazione risponde
  `422 databases_disabled` e il portale lo spiega. Si accende con `gitstack config set databases on`
  (vedi `operations.md`).
* **Permessi.** Creare, ridimensionare, eliminare, aprire la LAN, ruotare la password, fare backup e
  collegare un'app richiedono `admin` sull'owner. Chi non è admin non vede il database (404).
* **Solo TLS.** Le connessioni senza TLS sono rifiutate; l'autenticazione è `scram-sha-256`.

Si usa dal portale (voce **Databases**), dalla CLI (`gs db …`) o dall'API (`/databases/{owner}/{nome}`).
Gli esempi qui sotto usano la CLI.

## Creare un database

```sh
gs db create shop --pg 17 --owner acme                 # 1 GB, 100m di CPU, 256 MB di RAM
gs db create acme/shop --pg 16 --size 5GB --cpu 500m --memory 512MB
gs db list acme
gs db view acme/shop                                   # stato, uso, collegamenti
```

* `name` è una label DNS (minuscole, cifre, trattini) unica per owner (`409 name_conflict`).
* La **major** (16 o 17) si sceglie alla creazione e **non si cambia sul posto**: per passare a un'altra
  major si crea un database nuovo da un dump (sotto, «Cambiare major»).
* Il database parte in qualche decina di secondi (`status` da `creating` a `running`).
* **La password dell'utente `<nome>_owner` si vede una volta sola**, nella risposta di creazione (la CLI
  la stampa e il portale la mostra dopo un'azione esplicita). Non si rilegge mai: se la perdi,
  `gs db credentials rotate acme/shop` ne genera una nuova.
* Ridimensionare: `gs db resize acme/shop --size 10GB --cpu 1 --memory 1GB`. Il disco si può solo
  ingrandire.
* Ruoli: `<nome>_owner` (proprietario degli oggetti, non superuser), `<nome>_ro` (sola lettura,
  facoltativo). `gitstack_admin` esiste solo per GitStack, da socket locale.

## Collegare un database a un'app

Il collegamento serve `admin` sull'app e sull'owner, e l'app deve essere dello **stesso owner** del
database (anche di un altro repo; altrimenti `422 owner_mismatch`).

```sh
gs db link acme/shop --app acme/web/api                # lettura e scrittura
gs db link acme/shop --app acme/web/report --mode ro   # sola lettura
gs db link acme/shop --list
gs db unlink acme/shop --app acme/web/report --yes
```

GitStack crea per ogni collegamento un **ruolo proprio** (mai condiviso) e dà all'app, come segreti
gestiti, `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE=verify-full` e
`PGSSLROOTCERT`. L'app riparte con le variabili e si connette con `DATABASE_URL`, senza altra
configurazione. Solo le app collegate raggiungono la 5432; chi si collega deve ritentare la connessione
all'avvio (per qualche secondo il pod nuovo non è ancora ammesso). Il dettaglio (variabili in conflitto,
rotazione, ordine dello scollegamento) è in [`apps.md`](apps.md), «Collegare un database gestito
all'app».

Per cambiare la password di un collegamento: `POST …/links/{id}/rotate` (portale: scheda del
collegamento). L'app riparte con la password nuova.

## Backup e ripristino

Ogni database ha un dump `pg_dump -Fc` **cifrato** (AES-256-GCM):

* **programmato**: uno al giorno, gli ultimi 7;
* **su richiesta**: `gs db backup run acme/shop`, gli ultimi 5;
* **di sicurezza** (prima di un ripristino sul posto o di un aggiornamento minore): gli ultimi 3.

```sh
gs db backup list acme/shop
gs db backup run acme/shop
gs db backup restore acme/shop <id-backup> --yes                    # sul posto
gs db backup restore acme/shop <id-backup> --new --name shop-copia --yes
```

* **Ripristino sul posto**: serve la conferma (`owner/nome`), GitStack prende prima un dump di
  sicurezza e poi ripristina in una sola transazione (se fallisce, il database resta com'era).
  Gli oggetti nel dump vengono sostituiti; quelli creati dopo e non nel dump restano.
* **Ripristino in un database nuovo**: stesso owner e major, anche con nome diverso; ha password nuove.
* I **GRANT personalizzati** fatti sugli oggetti non sopravvivono al ripristino (`--no-acl`): il
  proprietario li rifà. I privilegi di `<nome>_ro` sono riapplicati.
* Un backup fallito **non è mai silenzioso**: resta in elenco con l'errore e il database lo mostra.
* `sudo gitstack backup` dell'amministratore include un dump fresco di ogni database; con
  `gitstack restore` su un'installazione pulita i database tornano con i dati, le app collegate sono
  ricollegate con password nuove. I volumi dei database non sono nel backup dei volumi.

## Accedere dalla LAN con pgAdmin o DBeaver

Di default un database è raggiungibile solo dalle app collegate. Per collegarti dal tuo PC:

1. Accendi l'accesso LAN: `gs db access lan acme/shop on` (o l'interruttore nel portale). GitStack
   assegna una **porta TCP propria** dall'intervallo dell'installazione (default `15432-15531`) e mostra
   l'indirizzo, la porta e il comando per il firewall.
2. **Chi amministra l'host** apre quella porta nel firewall, alla sola tua rete (GitStack non tocca
   mai ufw): `sudo ufw allow from <rete> to any port <porta> proto tcp`. Non esiste un filtro per IP
   sorgente dentro GitStack.
3. Scarica la **CA** dell'istanza (`/help/ca`, oppure `gs db connect` la salva per te) e usala per
   verificare il server.

Da riga di comando: `gs db connect acme/shop` apre `psql` con TLS e CA già a posto (serve `psql`
sul PC); `gs db connect acme/shop -- -c 'select 1'` passa argomenti a psql.

**pgAdmin** (Register → Server):

| Campo | Valore |
|---|---|
| Host name/address | l'indirizzo mostrato (quello con cui è stata creata l'istanza) |
| Port | la porta LAN del database |
| Maintenance database | il nome del database |
| Username | `<nome>_owner` |
| Password | quella mostrata alla creazione o alla rotazione |
| SSL → SSL mode | `verify-full` |
| SSL → Root certificate | il file `ca.crt` dell'istanza |

**DBeaver** (Nuova connessione → PostgreSQL): Host, Port, Database e Username come sopra; scheda
**SSL** → *Use SSL*, *SSL mode* `verify-full`, *CA Certificate* = `ca.crt`.

* L'indirizzo deve essere uno dei nomi o IP con cui è stato emesso il certificato: con un altro,
  `verify-full` fallisce con «does not match host name». Con `sslmode=disable` o senza la CA il server
  rifiuta.
* Spegnere l'accesso LAN chiude subito la porta. Un database sulla LAN è una superficie di attacco:
  tieni il firewall stretto e non c'è rate limit.

## Migrare da un Postgres-app con volume a un database gestito

Se oggi hai un'app che esegue Postgres con un volume (vedi `apps.md`, «Contesto di sicurezza e immagini
PostgreSQL»), la migrazione è un dump e un ripristino. Prevedi una breve finestra di fermo delle scritture.

1. **Crea il database gestito** della stessa major (o più recente), con dimensione sufficiente:
   `gs db create acme/shop --pg 16 --size 10GB`. Salva la password.
2. **Ferma le scritture**: scala l'app che usa il Postgres a zero (o mettila in manutenzione).
3. **Fai il dump** dal Postgres-app, nel formato custom:

   ```sh
   pg_dump -Fc --no-owner --no-acl -h <host-del-postgres-app> -U <utente> <db> > shop.dump
   ```

4. **Ripristina nel database gestito** dal PC, con la LAN accesa (passi 1–3 della sezione LAN):

   ```sh
   PGSSLMODE=verify-full PGSSLROOTCERT=ca.crt PGPASSWORD=<password> \
     pg_restore --no-owner --no-acl --role=shop_owner -h <indirizzo> -p <porta> -U shop_owner -d shop shop.dump
   ```

   `--no-owner` e `--role=shop_owner` fanno sì che gli oggetti siano del proprietario del database.
   Rifai a mano i GRANT personalizzati.
5. **Collega l'app**: `gs db link acme/shop --app acme/web/api`. Cambia l'app perché legga
   `DATABASE_URL` (o `PG*`) invece dell'indirizzo del Postgres-app, e rimuovi le variabili vecchie: se
   l'app ha già una variabile con uno di quei nomi il collegamento è rifiutato (`409 variable_conflict`).
6. **Verifica** i dati, poi spegni la LAN (`gs db access lan acme/shop off`) e, quando sei sicuro,
   elimina l'app Postgres vecchia e il suo volume (l'eliminazione del volume è definitiva).

## Cambiare major o aggiornare

* **Minor**: solo su richiesta, dopo un `gitstack upgrade` che porta un'immagine nuova
  (`POST /databases/{owner}/{nome}/upgrade-minor`). GitStack prende un dump di sicurezza e, solo se
  riesce, riavvia il database con l'immagine nuova sullo stesso volume.
* **Major**: mai sul posto. Fai un backup, creane uno nuovo con la major desiderata
  (`gs db create acme/shop17 --pg 17`), ripristina il dump nel nuovo database
  (`gs db backup restore … --new` mantiene la major: usa invece `pg_dump`/`pg_restore` come nella
  migrazione sopra), ricollega l'app, poi elimina il vecchio.

## Eliminare

`gs db delete acme/shop` chiede di scrivere `owner/nome`. L'eliminazione è **immediata**, senza cestino:
spariscono namespace, volume, collegamenti, porta LAN e dump, e le app collegate perdono le variabili.
Il solo modo di tornare indietro è un backup dell'amministratore (`gitstack backup`).

## Errori frequenti

| Errore | Significato |
|---|---|
| `422 databases_disabled` | la funzione è spenta sull'istanza |
| `409 insufficient_capacity` | CPU, RAM o disco oltre la quota dell'owner |
| `409 name_conflict` | nome già usato da quell'owner |
| `422 owner_mismatch` | l'app è di un owner diverso dal database |
| `409 variable_conflict` | l'app ha già una variabile con un nome `PG*`/`DATABASE_URL` |
| `409 port_unavailable` | l'intervallo delle porte LAN è finito |
| `422 backup_unavailable` | manca la chiave dei dump o il volume `databases-data` |
