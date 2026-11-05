# ADR 0003 — v3-d: architettura dei database gestiti (PostgreSQL)

- Stato: proposto (GIT-549), da approvare dal board
- Data: 2026-11-05
- Precedenti: ADR 0002, controller apps dentro `core` [c_19be62e8655c7e83]; priorità e quote
  [c_d7134baf8d45eb3b]; namespace/RBAC/ClusterRole modello [c_463f6c838fe9bea8]; naming e TLS
  [c_fdebc6a7b1004b53]; ADR 0001 (classi di priorità)
- Prova: [`poc-managed-db/`](poc-managed-db/) (`run.sh`, `run-roles.sh`, `logs/esito.txt`, `logs/esito-roles.txt`, `logs/cnpg-misura.txt`)
- Item che implementano: GIT-550…GIT-558 (tabella delle regole in fondo)

## Contesto e scelte già fissate dal board

Queste non si discutono qui:

1. si ospita **solo PostgreSQL**;
2. il database è **dell'owner** (utente o organizzazione) e si collega alle app di **qualunque repo
   di quell'owner**;
3. di default lo raggiungono **solo le app collegate**; un **interruttore per database** lo apre alla
   **LAN** (TLS dalla CA interna e password).

Vale ancora «pochi servizi»: niente componente nuovo se `core` basta. Il nodo è unico (homehub),
quindi alta disponibilità e repliche non sono un obiettivo di v3-d.

Provenienza dei numeri. Ogni cifra di questo documento ha una fonte; dove manca c'è scritto «non
misurato». Le misure sono state prese con `kubectl top` (metrics-server di k3s) su k3d, sull'host di
sviluppo (Docker Desktop, 8 CPU, 23,5 GiB) e **non su homehub**: sono ordini di grandezza, un solo
campione per riga, non un benchmark.

## Decisioni, alternative e rischi

### 1. Controller: in `core`, non CloudNativePG

**Decisione.** Il reconciler `databases` vive in `services/core/internal/databases` come
`apps` (ADR 0002): legge il modello Postgres, crea e osserva con il client Kubernetes un
**StatefulSet** a 1 replica, il suo **PVC** (`volumeClaimTemplates`, `local-path`), un **Service**
ClusterIP e una **NetworkPolicy**, e pubblica stato ed eventi con l'outbox. È una goroutine con
coda, timeout e retry idempotenti, non un servizio. Riusa la verifica cosign (H10), le classi di
priorità e il modello di RBAC dell'ADR 0002: ClusterRole *modello* senza ClusterRoleBinding,
RoleBinding per namespace, VAP sul solo ServiceAccount di `core`.

**Alternativa scartata: operatore CloudNativePG** (v1.30.1, ultima release al 2026-11-05).
Fatti verificati:

| Voce | CloudNativePG 1.30.1 | Controller proprio | Fonte |
|---|---|---|---|
| Licenza | Apache-2.0 | codice di GitStack | `gh api repos/cloudnative-pg/cloudnative-pg --jq .license.spdx_id` |
| Oggetti da installare | 11 CRD, 7 ClusterRole, 2 webhook (mutating + validating), 1 Deployment; manifest da 1 293 413 byte | nessuna CRD; più verbi nel ClusterRole *modello* di `core` | `curl -L …/cnpg-1.30.1.yaml`, `grep -c '^kind: CustomResourceDefinition'` e `grep '^kind:'` (`cnpg-measure.sh`) |
| Immagini in più nel bundle offline | almeno 2: `ghcr.io/cloudnative-pg/cloudnative-pg:1.30.1` (operatore, anche init container di ogni istanza) e `ghcr.io/cloudnative-pg/postgresql:<tag>` per ogni major | 1: `postgres:17-alpine` (`16-alpine` è già nel chart: `postgres.image.tag` in `values.yaml`) | `logs/cnpg-misura.txt` («immagine postgres usata», «init container»); `deploy/gitstack/values.yaml` |
| Immagine di default | `postgresql:18.6-system-trixie`, **uid 26**: per 16/17 serve `imageName` o un `ImageCatalog` | 16/17 alpine, uid 70 | `logs/cnpg-misura.txt` («immagine postgres usata», «uid del pod») |
| Firma | la release pubblica `cnpg-1.30.1.sigstore.json` per il manifest; la **firma delle immagini non è stata verificata** (manca cosign nell'ambiente) | cosign sul digest come tutte le immagini GitStack (H10, `docs/operations.md` V5) | elenco asset di `gh api repos/cloudnative-pg/cloudnative-pg/releases/latest` |
| RAM/CPU a vuoto (k3d, 1 nodo) | operatore 13m / 24Mi (5m / 31Mi con un cluster); istanza Postgres 15m / 52Mi | pod Postgres 13–37m / 50Mi in 4 esecuzioni di `run.sh` (in una prima esecuzione, log non conservato, 119m subito dopo l'init) | `cnpg-measure.sh` e `run.sh`, righe «a vuoto» dei log |
| Tempo a «pronto» | operatore 78 s; cluster a 1 istanza 209 s (compresi i pull da ghcr.io) | StatefulSet pronto entro il rollout status di `run.sh` (non cronometrato) | `logs/cnpg-misura.txt` |
| Upgrade | l'operatore e le sue 11 CRD hanno un ciclo proprio: Helm non aggiorna le CRD di una cartella `crds/`; un aggiornamento dell'operatore riavvia le istanze per aggiornare l'instance manager | segue `gitstack upgrade` (chart + binario di `core`) | `cnpg-measure.sh` ha applicato il manifest con `--server-side`; il comportamento di upgrade è dalla documentazione del progetto e **non è stato provato qui** |
| Funzioni che da sole giustificano un operatore (repliche, failover, PITR con WAL archive) | sì | no | — |

Peso su un nodo singolo: la differenza misurata è modesta (circa 24–31 MiB di RAM per l'operatore,
istanza paragonabile: 52 contro 50 MiB), quindi **non è la RAM a decidere**. Decidono: le 2 immagini e
le 11 CRD in più da portare in un'installazione senza internet (`--registry-mirror`, `docs/operations.md`);
il ciclo di vita di un secondo prodotto da firmare, aggiornare e supportare; l'immagine di default con
uid 26 e Postgres 18; e il fatto che su un nodo solo repliche, failover e PITR non servono.

**Rischi accettati.** Il lifecycle (creazione, riavvio, cambio immagine, ridimensionamento
dichiarato, eliminazione) è codice nostro e va provato su k3d. Il ClusterRole di `core` cresce
(`statefulsets`, `persistentvolumeclaims`, `networkpolicies`, `services`, `secrets`, `pods/exec`,
`pods/log` nei soli namespace `gitstack-db-*`); si mitiga con la stessa VAP dell'ADR 0002 (solo
namespace con label `gitstack.io/managed-db=true`). Se un giorno servono repliche o PITR, CloudNativePG
resta la strada: il modello dati (database, link, backup per `pg_dump`) non dipende dal controller.
Condizione di riesame: nodi multipli o richiesta di PITR.

### 2. Immagine, contesto di sicurezza, versioni e aggiornamenti

**Decisione.** Immagine ufficiale `postgres:<major>-alpine` (majors ammesse **16 e 17**; l'esterno già
supportato è 15–17, `docs/operations.md`), per **digest** in una tabella major → digest nel chart,
verificata con cosign prima di ogni StatefulSet (H10). Il pod gira **non root** con
`runAsUser/runAsGroup/fsGroup 70`, `runAsNonRoot`, `seccompProfile: RuntimeDefault`,
`allowPrivilegeEscalation: false`, `drop: ALL`, `readOnlyRootFilesystem: true` con `emptyDir` su
`/var/run/postgresql` e `/tmp`, PGDATA in `/var/lib/postgresql/data/pgdata` (la radice del volume
`local-path` non accetta `chmod`: `docs/apps.md`, «Contesto di sicurezza e immagini PostgreSQL»). Il
namespace impone Pod Security `restricted`.

A differenza del consiglio di `docs/apps.md` per le app (immagine derivata con `USER postgres`),
**qui non serve un'immagine derivata**: avviata fin dall'inizio con uid 70, l'entrypoint ufficiale
salta `chown` e `gosu`. Provato: `run.sh` mostra «postgres gira come uid 70», PVC `local-path/Bound`
e un pod root rifiutato dal namespace.

*Minor.* Il digest di una minor arriva con la release di GitStack (tabella del chart). Si applica per
database su richiesta di chi lo amministra (o per tutti dall'amministratore dell'istanza dopo
`gitstack upgrade`): dump di sicurezza automatico, poi cambio immagine, stesso PVC, riavvio. Mai
automatica. *Major.* Mai automatica e mai sul posto: il passaggio 16→17 è un **database nuovo** creato
col dump del vecchio (stesso strumento del ripristino in un database nuovo, punto 5); i link si
spostano a mano. `pg_upgrade` richiederebbe i binari di entrambe le versioni nell'immagine. Una
release non può togliere una major finché un database la usa: lo verifica `gitstack upgrade` (preflight).

**Scartate.** Immagine Debian (uid 999, più grande: non provata qui); immagine derivata per ogni
major (un build in più da firmare per nulla); PostGIS e altre estensioni oltre a `contrib` (fuori
scopo: le servirà un item con la sua immagine); aggiornamenti minor automatici (un riavvio non
richiesto interrompe le app collegate).
**Rischi.** Alpine usa musl (differenze di collation rispetto a glibc: va scritto nella guida);
`readOnlyRootFilesystem` può rompere estensioni future; la tabella dei digest va tenuta
aggiornata a ogni release (ci pensa il job di pubblicazione del chart).

### 3. Modello delle risorse, quota, utenti e ruoli

**Risorsa.** `core.managed_dbs` (id, `owner_name` minuscolo come `app_owner_quotas`, `name`,
`pg_major`, `size_bytes`, `cpu_millis`, `memory_bytes`, `status`, `lan_enabled`, `lan_port`,
tempi). `name` è una label DNS normalizzata da `pkg/names`, **unica per owner**
(`name_conflict`); namespace `gitstack-db-<id>`. Valori proposti (modificabili in GIT-550/557, non
misurati su homehub): disco 1 GiB, richiesta CPU 100m e limite RAM 256Mi (quelli della PoC, che con
50 MiB a vuoto ci stanno), priorità `gitstack-apps`.

**Quota.** La stessa dell'owner per le app: CPU, RAM e disco entrano in `core.app_owner_quotas` e nel
calcolo H3/H9/H11 insieme ad app e volumi; oltre il tetto 409 `insufficient_capacity` con
`details.missing`, avviso `quota.warning` dall'80%. **Ridimensionare** significa cambiare la
dimensione dichiarata: `local-path` non espande i PVC e non applica la dimensione come limite fisico
(stessa nota di H3). Perciò il limite è **morbido**: il controller misura `pg_database_size` e dà avvisi
all'80% e al 100%, senza bloccare le scritture. Un limite duro richiederebbe un'altra StorageClass: fuori scopo.

**Ruoli.**
- `gitstack_admin`: superuser usato solo dal controller via `exec` sul socket locale (`local trust`
  dentro il container); `pg_hba` non lo ammette in rete.
- `<name>_owner`: proprietario del database, **non** superuser, con le password di creazione/LAN.
- `<name>_ro` (facoltativo): `pg_read_all_data`, per app o persone in sola lettura.
- un ruolo di login per ogni **collegamento** (punto 4).
La PoC prova `ro` (legge, `permission denied` in scrittura) ma crea l'`owner` come superuser per la
semplicità dell'immagine ufficiale: la separazione `gitstack_admin`/`<name>_owner` **non è provata** in
`run.sh` e diventa un test di GIT-551.

**Permessi.** Creare, eliminare, ridimensionare, accendere la LAN, ruotare, fare backup/ripristino e
collegare richiede `admin` sull'owner (l'utente stesso, o l'amministratore dell'organizzazione secondo
i permessi M-03 già in `core`). Chi non è admin dell'owner non vede il database (404, come i repo
privati). L'amministratore dell'istanza vede tutto e può eliminare.

**Scartate.** Database per repo (la scelta del board è per owner); quota separata dei database (due
contabilità per lo stesso disco); superuser all'owner (potrebbe cambiare `pg_hba`, creare estensioni
untrusted ed eseguire programmi sul nodo del container).
**Rischi.** Un owner con molti database satura il nodo: vale la quota. Il limite di disco morbido può
riempire il disco del nodo (monitoraggio e avviso, non protezione).

### 4. Collegamento app ↔ database

**Chi.** Serve `admin` sull'app (H5) **e** `admin` sull'owner del database; l'app e il database devono
avere lo **stesso owner** (altrimenti 422 `owner_mismatch`), ma i repo possono essere diversi. Scollega
chiunque abbia uno dei due permessi.

**Variabili iniettate** (segreti dell'app, mai nelle risposte API né nei log):
`DATABASE_URL` (`postgresql://utente:password@host:5432/db?sslmode=verify-full&sslrootcert=/etc/gitstack/db/ca.crt`),
`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE=verify-full`,
`PGSSLROOTCERT=/etc/gitstack/db/ca.crt`. La CA (pubblica) è montata da un ConfigMap nel namespace
dell'app. Se l'app ha già una variabile con uno di quei nomi il collegamento è rifiutato
(409 `variable_conflict`), come per il prefisso riservato `GITSTACK_`.

**Nome interno.** `pg.gitstack-db-<id>.svc.cluster.local:5432` (il Service `pg`): è nei SAN del
certificato, quindi `verify-full` passa (provato dal pod dell'app collegata). Un alias `ExternalName`
come H15 è scartato: il nome dell'alias non sarebbe nel certificato e `verify-full` fallirebbe.

**Rete.** Il controller mette sul namespace dell'app la label `gitstack.io/db-link-<id-db>=true`; la
NetworkPolicy `db-isolation` del namespace del database (ingress, `policyTypes: [Ingress, Egress]`,
nessuna uscita) ammette solo `namespaceSelector` con quella label, porta 5432. Funziona per namespace
di repo diversi perché seleziona per label, non per `repo-id`. Dal lato dell'app, la NetworkPolicy
`app-isolation` (H9/H16) riceve una regola di uscita verso i pod di quel solo database sulla 5432; le
barriere H16 (reti private chiuse) non si applicano a questa regola perché è per selettore, ma **la
PoC non la prova** (i suoi namespace client non hanno `app-isolation`): è un criterio di GIT-552 con un
test k3d dedicato. Il namespace dell'app contiene una sola app, quindi la label non allarga a terzi.

**Ruoli per collegamento (soluzione unica, senza ripiego).** Ogni collegamento ha il **proprio**
ruolo di login, senza `CREATEROLE`/`CREATEDB`/superuser: `rw` è membro di `<name>_owner` con
`ALTER ROLE l_<id> SET role = '<name>_owner'`, così ogni oggetto creato da quella sessione appartiene
al proprietario e il ruolo del link **non possiede nulla**; `ro` è membro di `<name>_ro`
(a sua volta membro di `pg_read_all_data`). Non esiste un ruolo condiviso per modalità: revoca e
isolamento sono per link. Provato in `run-roles.sh`: un oggetto creato da `l_rw1` risulta di
`shop_owner` e `DROP ROLE` non resta bloccato.

**Rotazione.** Password nuova, `ALTER ROLE … PASSWORD`, Secret riscritto, app riavviata. Le sessioni
aperte con la vecchia password restano valide finché non si chiudono: la rotazione di un link termina
anche le sue sessioni (stesso passo 2 qui sotto), così nessuna connessione sopravvive alla password vecchia.

**Ordine di scollegamento (obbligatorio, nell'ordine).** `ALTER ROLE`/`DROP ROLE` **non** chiudono le
connessioni già autenticate, e togliere la NetworkPolicy non è una garanzia portabile per quelle
esistenti (dipende dal conntrack del CNI). Quindi:
1. `ALTER ROLE l_<id> NOLOGIN` (nessun nuovo login);
2. `pg_terminate_backend(pid)` per tutte le sessioni con `usename = 'l_<id>'`, ripetuto finché
   `pg_stat_activity` non ne mostra più (con `NOLOGIN` non ne nascono di nuove);
3. `DROP OWNED BY l_<id>` e `DROP ROLE l_<id>`;
4. solo dopo: via variabili e Secret dall'app (riavvio), label sul namespace e regola di uscita.

Se il passo 2 non completa entro il timeout lo scollegamento resta `unlinking` e ritenta; il collegamento
non si dichiara rimosso finché il ruolo esiste. Provato in `logs/esito-roles.txt`: con il solo `NOLOGIN` la
sessione persistente di `l_ro1` **continua a eseguire query**; con l'ordine sopra la sessione persistente
di `l_rw1` riceve «terminating connection», smette di eseguire query, il nuovo login è respinto
(`role does not exist`) e la sessione persistente e le scritture di `l_rw2` (altro link) continuano.
La PoC k3d (`run.sh`) prova invece solo nuove connessioni; la persistente è coperta da `run-roles.sh`.

**Avvertenza osservata.** Un pod appena creato non è ammesso dalla NetworkPolicy per qualche secondo:
nelle prove, le connessioni subito all'avvio sono fallite (con «Connection refused») e dopo 5 secondi
dal Ready passavano (non cronometrato con precisione). `run.sh` aspetta 10 s prima di ogni `psql` per
questo. Le app devono ritentare la connessione all'avvio; la guida lo dirà.

**Scartate.** Password o ruolo condivisi tra le app o per modalità (una fuga non si isola, scollegare non revoca una sola app: era il ripiego della prima stesura, tolto); affidarsi alla sola rimozione della NetworkPolicy per chiudere le sessioni aperte (non portabile); un proxy
(PgBouncer) davanti: componente in più; consentire per `repo-id` (i database sono dell'owner, non del
repo). **Rischi.** Finestra di errori durante la rotazione (riavvio dell'app); la label sul namespace è
un privilegio del controller: la VAP deve permettere a `core` di scrivere solo le label `gitstack.io/db-link-*`.

### 5. Backup

**Decisione.** `pg_dump -Fc` (formato custom, compresso, snapshot consistente a database acceso) con
`exec` nel pod del database; la directory dati non si copia mai (a differenza dei volumi delle app: i
PVC dei database sono **esclusi** dal backup dei volumi, `admin/internal/backup/appvolumes.go`).
`pg_basebackup` è scartato: richiede connessione di replica, `pg_hba` dedicato e, per essere utile,
archiviazione WAL (PITR), che su un nodo solo non c'è.

- **Programmato.** `core` esegue un dump di ogni database una volta al giorno (orario d'istanza) e
  tiene gli ultimi **7**; **su richiesta** (`admin` dell'owner) ne tiene gli ultimi **5**, a parte.
  Sono file cifrati con AES-256-GCM (`NewEncryptWriter`, stesso formato di `admin/internal/backup/crypto.go`) con
  una chiave di dump dell'istanza: un Secret `helm.sh/resource-policy: keep`, la quinta chiave di
  lunga vita, inclusa in `secrets.json` di `gitstack backup`. Stanno su un volume `databases-data` di
  `core` (come `apps-data`). Non contano nella quota dell'owner ma hanno un tetto d'istanza con avviso.
- **In `gitstack backup`.** Ad ogni esecuzione (timer giornaliero) l'archivio riceve un dump *fresco*
  di ogni database, in `databases/<db-id>/<name>.dump`; è cifrato come l'archivio (obbligatorio con
  S3: `backup.go`, «gli archivi caricati su S3 sono sempre cifrati») e segue retention e S3 senza codice nuovo.
  La coerenza è per database, non fra database diversi né con il database di `core`.
- **Ripristino: ruoli, proprietà e privilegi.** Ogni database è la sua istanza PostgreSQL, quindi i
  ruoli (globali all'istanza) non stanno nel dump e **non si ripristinano da esso**. Il dump resta
  completo (`-Fc`, con owner e GRANT) ma si ripristina **sempre** con `--no-owner --no-acl
  --role=<name>_owner` e i privilegi si riapplicano in modo esplicito. Sequenza del controller,
  uguale per ogni destinazione (database nuovo con nome diverso, istanza vuota di `gitstack restore`,
  stesso database):
  1. ruoli, se mancano: `<name>_owner` (login, non superuser), `<name>_ro` (nologin, membro di
     `pg_read_all_data`); `CREATE DATABASE <name> OWNER <name>_owner`; `REVOKE ALL … FROM PUBLIC`,
     `GRANT CONNECT`, proprietà dello schema `public`;
  2. `pg_restore --no-owner --no-acl --role=<name>_owner`: gli oggetti nascono del nuovo proprietario
     e nessun GRANT/`ALTER DEFAULT PRIVILEGES` del dump cita i vecchi ruoli;
  3. privilegi riapplicati: `GRANT SELECT ON ALL TABLES` e `ALTER DEFAULT PRIVILEGES … GRANT SELECT` a
     `<name>_ro` (idempotente, è la stessa routine della creazione);
  4. link: ruoli di link ricreati con **password nuove** (non stanno nei backup), `rw`/`ro` come
     da modello, Secret delle app riscritti e app riavviate.

  *Stesso database* (conferma scrivendo `owner/nome`, dump di sicurezza automatico): stessa sequenza con
  `--clean --if-exists`; i ruoli esistono già e si salta il passo 1. *Database nuovo* (anche con nome
  diverso): è un'istanza nuova, il nome della destinazione sostituisce `<name>` nei ruoli.
  *`gitstack restore`*: dopo il ripristino di `core` il controller ricrea le istanze vuote e applica
  la sequenza ai dump dell'archivio; nessun segreto di database viaggia nei backup.
  **Alternative scartate:** ripristinare con owner e ACL originali (fallisce con `role "shop_owner" does
  not exist` in un'istanza con nome diverso: provato nel controllo negativo di `run-roles.sh`);
  ricreare i vecchi nomi di ruolo nella destinazione (porta nomi del database vecchio in quello nuovo e
  collide con il nome richiesto); rimappare owner/ACL con edit del dump (fragile).
  **Rischi:** `--no-acl` perde i GRANT personalizzati dell'owner su oggetti (restano solo owner e `ro`):
  va scritto nella guida e l'owner può rifarli; `ALTER ROLE … IN DATABASE`/`ALTER DATABASE SET` non sono
  nel dump e non si ripristinano. Provato in `run-roles.sh` su istanza vuota con nome diverso e con lo
  stesso nome (vedi DB15).

**Scartate.** Dump in un CronJob per database (pod, immagini, NetworkPolicy e ruolo di rete in più);
copia del PVC (non consistente a database acceso; incluso solo per i volumi delle app, che l'UI già
avvisa «sembra un database»); dump non cifrati sul volume.
**Rischi.** `exec` nei pod è un privilegio forte: il RoleBinding è per i soli namespace `gitstack-db-*`.
Il dump giornaliero di database grandi pesa su un nodo solo (non misurato). Il fake `backup.Cluster`
è copiato in 4 posti: un metodo nuovo va aggiunto a tutti.

### 6. Accesso dalla LAN

**Decisione.** Interruttore per database, **spento di default**. Acceso: `core` assegna al database
una porta TCP da un intervallo configurabile (proposta: 15432–15531, non una misura) e crea un
Service `LoadBalancer` (ServiceLB di k3s, la stessa tecnica della porta SSH di git,
`templates/git/service.yaml`) più la NetworkPolicy `db-lan` (ingresso da `0.0.0.0/0` salvo il CIDR dei
pod). PostgreSQL fa TLS da sé con un certificato firmato dalla CA interna, `pg_hba` ammette solo
`hostssl` con `scram-sha-256` e rifiuta `hostnossl`. I client usano `sslmode=verify-full` con la CA.
Spento: Service e policy spariscono e la porta si chiude.

**Provato su k3d** (`logs/esito.txt`): `psql` 17 da fuori dal cluster, `sslmode=verify-full`
accettato con la CA privata; respinti `sslmode=disable` («rejects connection»), assenza della CA
(«certificate verify failed»), un hostname sbagliato («does not match host name») e la password
sbagliata; con la LAN accesa un'app *non* collegata resta respinta in cluster; spenta, la porta non
risponde. La policy `db-lan` è **necessaria**: senza, il traffico della LAN era scartato da
`db-isolation` (esito di una esecuzione senza `db-lan`, log non conservato: «server closed the connection unexpectedly»).

**Certificati.** `core` non ha la chiave della CA (resta sull'host, ADR 0002). Il certificato del
database lo firma un'**intermedia dei database**, creata dall'installer dalla radice, con
`nameConstraints` (solo `*.svc.cluster.local` e i nomi/IP dell'istanza), in un Secret leggibile solo da
`core`; il certificato del server porta foglia + intermedia, i client si fidano della sola radice già
distribuita (H2). Rinnovo prima dei 30 giorni dalla scadenza con ricarica del Secret (`pg_reload_conf`,
niente riavvio). **Non provato in `run.sh`**, che usa una CA piatta e solo SAN DNS: la catena con
l'intermedia, le name constraints e il caso `verify-full` su un SAN IP (installazione con solo IP) sono
criteri di GIT-557/554.

**Alternative scartate.**
- *Traefik `IngressRouteTCP` con SNI su una porta sola:* Postgres 16 e client precedenti aprono con
  `SSLRequest` in chiaro e poi TLS, quindi Traefik non vede lo SNI. Con PG17 e `sslnegotiation=direct`
  (libpq 17) sarebbe possibile, ma solo per client 17+ e con un DNS wildcard; **non provato**, resta
  un'evoluzione futura.
- *NodePort:* intervallo 30000–32767 fisso, scomodo da aprire e da ricordare; ServiceLB riusa il modello già in produzione.
- *Terminare il TLS in Traefik o in un proxy:* sposta la CA e la password in un componente in più.

**ufw e IP.** L'installer non modifica ufw (`deploy/README.md`) e `core` non può farlo. La console e
`gs db` mostrano il comando per aprire la porta (`ufw allow from <rete> to any port <porta> proto
tcp`). **Non provato:** se ufw filtri davvero il traffico di ServiceLB (instradato via DNAT, quindi
dalla catena FORWARD e non da INPUT) va controllato su homehub: k3d non ha ufw. Con un IP solo l'unico
discriminante è la porta, quindi un database per porta e nessun nome host per database. Nella PoC
Postgres ha visto **10.42.0.1** come sorgente di tutti i client LAN (il nodo, per SNAT): **non c'è
allow-list per IP sorgente** in v3-d; servirebbe `externalTrafficPolicy: Local` (non provato).
**Rischi.** Un database sulla LAN è una superficie di attacco: password forte generata (32 caratteri
esadecimali), solo TLS, rate limit non previsto (avviso in guida); le porte finiscono; collisioni con
altre porte del nodo (`409 port_unavailable`, controllo con `ss` nel preflight).

### 7. Eliminazione, audit, eventi, metriche e log

**Eliminazione.** Immediata con conferma scrivendo `owner/nome`, **senza cestino**, come i repo
(R2, GIT-530): spariscono namespace, StatefulSet, PVC, link (le app perdono variabili e riavviano),
porta LAN e i dump programmati e su richiesta; il nome si libera alla risposta. Resta solo il
backup (`gitstack backup`). Il cestino è scartato: tiene dati e quota di un database che il
proprietario ha detto di volere eliminato, e il board ha già deciso così per i repo.
**Audit.** Ogni azione (crea, ridimensiona, collega, scollega, ruota, LAN, backup, ripristina, elimina)
è registrata con chi e quando, mai con le password.
**Eventi e webhook.** Famiglia `database.*` (`created`, `updated`, `deleted`, `linked`, `unlinked`,
`lan_enabled`, `lan_disabled`, `backup_completed`, `backup_failed`, `restored`) e `quota.warning`
dalle stesse regole delle app; i payload non contengono segreti. Nomi e schema esatti in GIT-550
(`docs/events.md`).
**Metriche e log.** CPU/RAM da metrics-server (se manca, «non disponibile», come le app), dimensione
logica da `pg_database_size` a intervalli, riavvii dallo stato del pod; log di Postgres da `pods/log`
con `admin` dell'owner, conservati come i log delle app su `apps-data`.
**Rischi.** Eliminare è irreversibile: la conferma deve restare nel contratto (nessuna scorciatoia per
la CLI senza `--yes`, G8).

## Regole DB1…DB24, verifica e item

Ogni regola va riportata in `docs/rules-coverage.md` e in Prisma (GIT-558). «Verifica» è quella che
l'item deve consegnare.

| Regola | Enunciato | Verifica | Item |
|---|---|---|---|
| DB1 | Un database è di un owner, solo PostgreSQL 16 o 17; `name` è una label DNS unica per owner (`name_conflict`) | test di contratto + `TestDatabases_Validazione` | GIT-550, GIT-551 |
| DB2 | Crea, elimina, ridimensiona, LAN, ruota, backup e collega richiedono `admin` sull'owner; chi non lo è non vede il database (404) | `TestDatabases_Permessi` (integration) | GIT-551 |
| DB3 | CPU, RAM e disco contano nella stessa quota dell'owner delle app; oltre il tetto 409 `insufficient_capacity`, avviso dall'80% | `TestDatabases_QuotaCondivisaConLeApp` | GIT-551 |
| DB4 | Un database è un namespace `gitstack-db-<id>` (label `managed-db`, Pod Security `restricted`) con StatefulSet a 1 replica, PVC `local-path`, Service e NetworkPolicy; nessun ClusterRole per i database | test dei manifest, `TestDatabases_IsolamentoSuK3d`, `deploy/tests/chart_hardening_test.sh` | GIT-551, GIT-557 |
| DB5 | Immagine `postgres:<major>-alpine` per digest, non root (uid 70), `drop: ALL`, root fs in sola lettura, seccomp; cosign verificato prima dello StatefulSet | test dei manifest e del gate cosign | GIT-551, GIT-557 |
| DB6 | Minor solo su richiesta (dump di sicurezza prima); major mai sul posto (nuovo database da dump); `gitstack upgrade` non toglie una major in uso | test del controller + test del preflight di upgrade | GIT-551, GIT-557 |
| DB7 | `gitstack_admin` superuser solo da socket locale; `<name>_owner` non superuser; `<name>_ro` facoltativo | integration su k3d (query sui ruoli) | GIT-551 |
| DB8 | Solo TLS: `hostssl` `scram-sha-256`, `hostnossl` rifiutato; password casuale in Secret | `run.sh` (sslmode=disable respinto) + test k3d | GIT-551 |
| DB9 | Il collegamento richiede `admin` sull'app e sull'owner, stesso owner (`owner_mismatch`), repo anche diversi; un ruolo di login **proprio** per collegamento (mai condiviso), senza `CREATEROLE`: `rw` membro di `<name>_owner` con `SET role` all'owner, `ro` membro di `<name>_ro`; gli oggetti creati sono del proprietario | `TestDatabaseLinks_PermessiEOwner` + `TestDatabaseLinks_ProprietaOggetti` (oggetto creato dal link = proprietà dell'owner; `DROP ROLE` riesce); logica già provata da `run-roles.sh` | GIT-551, GIT-552 |
| DB10 | Variabili `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE`, `PGSSLROOTCERT` come segreti dell'app; mai in API e log; conflitto di nome 409 | test dei manifest e delle risposte API | GIT-552 |
| DB11 | Solo le app collegate raggiungono la 5432, anche da repo diversi; regola di uscita dell'app verso quel database; scollegare o eliminare l'app toglie label e regola | `TestDatabases_CollegamentoSuK3d` (collegata sì, non collegata e altro namespace no, scollegata no) **più** `TestDatabaseLinks_SessionePersistenteChiusaAlloScollegamento` (k3d): una connessione persistente aperta prima dello scollegamento esegue query in loop; dopo lo scollegamento deve ricevere «terminating connection» e smettere, mentre la connessione persistente di un altro link continua | GIT-551, GIT-552 |
| DB12 | La rotazione cambia la password, riscrive il Secret, riavvia l'app e termina le sessioni del link; la vecchia è respinta. Lo scollegamento segue l'ordine: `NOLOGIN` → `pg_terminate_backend` fino a zero sessioni → `DROP OWNED`/`DROP ROLE` → poi variabili, Secret, label e regola di uscita; resta `unlinking` finché il ruolo esiste | k3d + integration (nuove connessioni come `run.sh`, persistenti come `run-roles.sh`; il test dell'ordine deve fallire se `pg_terminate_backend` manca) | GIT-551, GIT-552 |
| DB13 | Dump `pg_dump -Fc` cifrato AES-256-GCM, programmato giornaliero (ultimi 7) e su richiesta (ultimi 5); mai la directory dati | integration di backup + `TestDatabaseBackup_Retention` | GIT-553 |
| DB14 | `gitstack backup` mette un dump fresco di ogni database in `databases/<id>/<nome>.dump` (cifrato, S3 sempre cifrato); i PVC dei database non entrano nel backup dei volumi | `admin/internal/backup` test + test S3 MinIO | GIT-553 |
| DB15 | Ripristino sempre con `--no-owner --no-acl --role=<name>_owner`, ruoli ricreati dal controller e privilegi `ro` riapplicati; nello stesso database (conferma + dump di sicurezza, `--clean --if-exists`), in uno nuovo anche con nome diverso, e in `gitstack restore`; link ricreati con password nuove | `TestDatabaseRestore_IstanzaVuotaNomeDiverso` e `TestDatabaseRestore_IstanzaVuotaStessoNome` (integration, stesse asserzioni di `run-roles.sh`: restore senza errori, owner non superuser corretto, link `rw` scrive, `ro` legge e non scrive, nessun riferimento ai vecchi ruoli in ACL e dump dello schema) + passo e2e su VM | GIT-553, GIT-558 |
| DB16 | LAN spenta di default; accesa assegna una porta dell'intervallo (`port_unavailable`), Service `LoadBalancer` + `db-lan`; spenta chiude | `TestDatabaseLAN_*` su k3d | GIT-554 |
| DB17 | TLS LAN: `verify-full` con la CA privata accettato, respinto senza CA, con hostname sbagliato, con `sslmode=disable`; foglia firmata dall'intermedia con name constraints; rinnovo prima dei 30 giorni | `psql` in k3d (come `run.sh`), `openssl verify -verify_hostname`, test del rinnovo | GIT-554, GIT-557 |
| DB18 | Nessuna modifica di ufw; console e CLI mostrano il comando; nessuna allow-list per IP sorgente in v3-d | test UI/CLI + guida; verifica manuale su homehub (e2e) | GIT-554, GIT-558 |
| DB19 | Eliminazione immediata con conferma `owner/nome`, senza cestino; rimuove namespace, PVC, link, porta LAN, dump; le app collegate perdono le variabili | `TestDatabases_Eliminazione` + k3d | GIT-551, GIT-552 |
| DB20 | Audit di ogni azione e eventi `database.*` senza segreti, con webhook | test di contratto degli eventi + integration | GIT-550, GIT-551 |
| DB21 | Metriche (CPU/RAM/dimensione/riavvii) e log del database con `admin` dell'owner; «non disponibile» se manca metrics-server | integration + test API | GIT-551 |
| DB22 | La password si vede solo alla creazione o rotazione e non si rilegge; UI e CLI lo riflettono | test del contratto, `gs db` e UI | GIT-550, GIT-555, GIT-556 |
| DB23 | I database sono spenti nell'installazione di default: `databases.enabled`, `gitstack config set databases`, intervallo porte e preflight di risorse | `deploy/tests` del chart + test di `admin/` | GIT-557 |
| DB24 | Ogni regola DBn compare in `docs/rules-coverage.md` con test reali (nessuna «coperta» senza il test) e in Prisma; guida per homehub | `go run scripts/check-rules-coverage.go` + e2e su VM | GIT-558 |

## Impatto sul piano

Il numero di item resta 9 (GIT-550…558). Cambia l'**ordine**:

1. **GIT-550** (contratto) per primo, come previsto.
2. **GIT-557 va prima di GIT-551, almeno nella parte chart/installer**: la ClusterRole *modello* con la
   VAP, la tabella dei digest, l'intermedia dei database con name constraints (serve a ogni database
   fin dalla creazione, non solo alla LAN) e la chiave di dump. Proposta: spezzare GIT-557 in
   «chart/RBAC/CA» (prima di GIT-551) e «impostazioni d'istanza e console» (dopo).
3. GIT-551, poi GIT-552 e GIT-553 (indipendenti fra loro), poi GIT-554 (usa l'intermedia e il
   Service/policy), poi GIT-555/556 (portale e CLI), GIT-558 per ultimo.
4. GIT-551 è il più pesante (controller, ruoli, quota, eliminazione): **ridimensionamento** vuol dire
   solo dimensione dichiarata e avvisi (punto 3), non espansione del PVC. Il titolo di GIT-551 lo
   promette: va letto così.
5. Dopo la revisione di GIT-549: DB9/DB11/DB12 hanno ora test anche in GIT-551 (ruoli, proprietà degli
   oggetti, ordine di scollegamento con sessioni persistenti, riusati da GIT-552) e DB15 ha due test
   di restore su istanza vuota in GIT-553. Né numero né ordine degli item cambiano per questo.
6. GIT-553 dipende dal Secret della chiave di dump (parte chart di GIT-557) e da un metodo nuovo
   nell'interfaccia `backup.Cluster` (4 fake da aggiornare).

## Prova di fattibilità

[`poc-managed-db/run.sh`](poc-managed-db/run.sh), lanciato da questa cartella con `bash run.sh` (serve
Docker, k3d, kubectl, openssl); termina con righe `OK`/`KO`, `ESITO` ed `exit`, e cancella il cluster.
Cosa fa: k3d con la porta 15432 mappata sul load balancer; CA con date retrodatate (l'orologio della
VM di Docker Desktop era indietro di circa 2 minuti e un certificato valido «da adesso» dava
`certificate verify failed`); namespace `gitstack-db-poc` con Pod Security `restricted`; StatefulSet
`postgres:17-alpine` non root con PVC `local-path`; due namespace client (uno collegato, uno no);
`psql` da pod; dump e ripristino; rotazione; scollegamento; porta LAN con `psql` 17 dall'host.
Esito completo e SHA in `logs/esito.txt`; ruoli, scollegamento e ripristino in `run-roles.sh` / `logs/esito-roles.txt`. La misura di CloudNativePG è
`CNPG_MANIFEST=<cnpg-1.30.1.yaml> bash cnpg-measure.sh`, esito in `logs/cnpg-misura.txt`.

Provato da `run-roles.sh` (tre istanze PostgreSQL 17 in Docker, `logs/esito-roles.txt`; comando
`bash docs/adr/poc-managed-db/run-roles.sh`, serve solo Docker): ruolo owner non superuser,
`ALTER ROLE … SET role`, ordine di scollegamento con sessioni persistenti e ripristino in istanza vuota
(nome diverso e stesso nome).

Non provato nella PoC (da chiudere negli item): gli stessi controlli su k3d con le immagini del chart
(`run-roles.sh` usa Docker semplice), regola di uscita di `app-isolation` verso il database, catena CA intermedia con name constraints, SAN
IP, ufw e FORWARD su un host vero, `externalTrafficPolicy: Local`, firma cosign delle immagini,
capacità con molti database, Debian/uid 999, comportamento su homehub (RAM/CPU reali).

## Rischi trasversali aperti

- Il ClusterRole di `core` e `pods/exec` nei namespace dei database sono privilegi critici: il chart
  deve impedirne la scrittura alle app e la VAP deve restringere le label scrivibili.
- Nodo unico: un database che riempie il disco o la RAM danneggia gli altri servizi; la priorità e la
  quota mitigano, non eliminano.
- Gli errori di connessione ai pod nuovi (NetworkPolicy che impara gli IP in qualche secondo) toccano
  anche le app dopo ogni deploy: la guida deve dire di ritentare.
- Backup per `pg_dump`: nessun PITR; la perdita massima è fino all'ultimo dump (24 ore di default).
