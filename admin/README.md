# admin/ — `gitstack`, il comando di amministrazione dell'host

`gitstack` è il binario Go con cui l'amministratore gestisce l'installazione
di GitStack **sull'host** (stato, backup e restore oggi; upgrade e config nei
prossimi item di M-08 [c_8458909a21d9035f]). Non è `gs`: `gs` è la CLI degli
utenti (`cli/`, M-07), `gitstack` è dell'host e parla con il cluster.

## Perché un modulo a sé

Decisione del CTO (GIT-142): un modulo Go nuovo del workspace,
`github.com/fathorMB/GitStack/admin`, binario in `admin/cmd/gitstack`.

- **Non in `cli/`**: `cli/` è Apache-2.0, pensato per gli utenti e gli agenti
  di coding, e deve restare piccolo e senza dipendenze di amministrazione.
  Questo strumento è del prodotto server: **AGPL-3.0** (`admin/LICENSE`), come
  `services/`.
- **Non in `services/`**: non è un servizio, non ha Dockerfile e non entra
  nel check `go-standalone-build`.
- Dipende solo da `gopkg.in/yaml.v3`; nessuna dipendenza da `cli/` o dai
  servizi.

## Costruire e installare

```bash
# Dalla radice del repo (Go come in go.work). Versione = tag immagine del server.
cd admin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=sha-<commit>" -o gitstack-linux-amd64 ./cmd/gitstack
sha256sum gitstack-linux-amd64 > gitstack-linux-amd64.sha256
```

`deploy/install.sh` lo installa in `/usr/local/bin/gitstack` **verificando lo
SHA-256** (V5): se il checksum non torna esce con errore, prima di toccare k3s
e il chart, e non installa niente.

```bash
sudo ./deploy/install.sh --admin-binary ./admin/gitstack-linux-amd64
# il checksum si legge da ./admin/gitstack-linux-amd64.sha256, oppure:
sudo ./deploy/install.sh --admin-binary <percorso|URL> --admin-sha256 <hex>
```

### Da dove arriva il binario

Nel percorso standard (`curl | sudo bash`, quello del cliente) l'installer
scarica da solo `gitstack-linux-amd64` e `gitstack-linux-amd64.sha256` dalla
release GitHub `sha-<commit>`, lo stesso tag delle immagini (`image_tag`):
`…/releases/download/sha-<commit>/gitstack-linux-amd64`. Li pubblica il job
`admin-binary` di `.github/workflows/ci.yml`, che gira **solo dopo il merge**
(push su main o tag, mai sulle PR, come il job `registry`): finché la CI di quel
commit non è finita, la release non esiste. Il `.sha256` è nel formato di
`sha256sum` (primo campo = hash), quello che legge `install.sh`. Se il binario
non c'è l'installer avvisa («binario di gitstack non pubblicato») e prosegue senza
installarlo; `GITSTACK_ADMIN_REQUIRED=1` (lo imposta `e2e.ps1`) rende l'assenza
un errore.

Per un checkout locale, o un commit non ancora su main, si costruisce il
binario come sopra e si passa `--admin-binary`.

Rilanciare l'installer aggiorna il binario se differisce da quello installato
(sostituzione atomica) e riscrive il file di configurazione.

Sulla VM di test (`deploy/test-vm`) `e2e.ps1` usa il percorso standard (binario
della release `sha-<Ref>`) e prova `sudo gitstack status`; per un commit non
ancora pubblicato vedi `deploy/test-vm/README.md`.

## File di configurazione: `/etc/gitstack/config.yaml`

Scritto da `install.sh` (root, `0600`, cartella `0700`), letto dai comandi.
Un file leggibile da gruppo o altri viene rifiutato. Percorso alternativo:
`--config` o `$GITSTACK_CONFIG`.

```yaml
version: 1                       # versione dello schema
host: 10.0.0.5                   # nome o IP con cui si raggiunge GitStack (senza schema)
ssh_port: 2222                   # porta SSH del servizio git (mai la 22 dell'host)
tls: internal                    # internal | custom | letsencrypt | insecure (assente = HTTP, installazioni precedenti)
ca_cert: /etc/gitstack/tls/ca.crt  # solo con tls: internal; la chiave (ca.key) sta accanto, solo root
chart_dir: /usr/local/share/gitstack/chart  # copia del chart Helm (serve a `config set host`)
release: gitstack                # release Helm
namespace: default               # namespace Kubernetes
image_tag: sha-<commit>          # tag immagine installato (versione del server)
kubeconfig: /etc/rancher/k3s/k3s.yaml
backup:
  destination: /var/backups/gitstack   # cartella dei backup (percorso assoluto)
  retention: 7                         # quanti backup conservare (>= 1)
  # s3:                                # facoltativo (N7): copia cifrata su S3
  #   endpoint: s3.example.com         # host[:porta], o URL con schema
  #   bucket: gitstack-backups
  #   prefix: prod
  #   region: eu-central-1             # default us-east-1
  #   access_key: ...                  # solo root: il file è 0600
  #   secret_key: ...
  #   key_file: /etc/gitstack/backup.key   # obbligatoria: archivi sempre cifrati
  #   retention: 30                    # archivi su S3 (default 30) oppure "bucket"
  #   notify_command: 'mail ...'       # eseguito con sh -c se il caricamento fallisce
network:                         # facoltativa: rete aziendale (N9), scritta da install.sh
  http_proxy: "http://proxy.corp:3128"
  https_proxy: ""                # vuoto = quello HTTP
  no_proxy: "registry.corp"      # si aggiunge a reti del cluster e indirizzi del nodo
  registry_mirror: "https://registry.corp:5000"   # tutte le immagini (registries.yaml di k3s)
  registry_username: "puller"    # insieme a registry_password (il file è root-only, 0600)
  registry_password: "..."
  download_mirror: "https://files.corp/gitstack"
  ca_bundle: /etc/gitstack/network/ca.crt         # copia gestita della CA aziendale
```

**Backup su S3 (N7).** Con `backup.s3` il backup giornaliero crea l'archivio in
locale e poi lo carica sul bucket (AWS, MinIO, B2, Wasabi: client minio-go).
Gli archivi sono sempre cifrati con la chiave di `key_file` (la stessa di
`--key-file`): una sezione senza `key_file` è rifiutata dal caricamento del
config, da `gitstack config set backup.s3` e da `install.sh`. Le chiavi S3 non
compaiono mai in log, stato o notifiche. Conservazione su S3 separata da quella
locale; `retention: bucket` non cancella niente. Se il caricamento fallisce il
backup locale resta valido, il comando esce con 10, `gitstack status` mostra
`Backup su S3: ULTIMO CARICAMENTO FALLITO` e gira `notify_command` (variabili
`GITSTACK_BACKUP_EVENT/ARCHIVE/ERROR`). Scelta: niente API interna di core, il
tool gira sull'host fuori dal cluster e non ha credenziali per essa.
`gitstack restore --from s3 [nome]` scarica (il più recente, o il nome dato) e
ripristina, anche su una macchina nuova dopo `install.sh --s3-*`.

Con un Postgres esterno (N8) l'installer aggiunge la sezione:

```yaml
postgres:
  external: true
  host: db.example.com
  port: 5432
  database: gitstack
  user: gitstack
  ssl_mode: verify-full
  version: "16.4"                          # rilevata dal preflight dell'installer
  password_secret: gitstack-postgres-external
  ca_secret: gitstack-postgres-ca          # facoltativo
```

Catena di fornitura (V5, GIT-209): l'installer scrive sempre la sezione

```yaml
v5:
  allow_unsigned: false                      # true = eccezione N9 (mirror interno non firmato)
  cosign_path: /usr/local/bin/gitstack-cosign   # il cosign pinnato che ha installato
```

`allow_unsigned: true` è l'eccezione N9: la scrive solo `install.sh --allow-unsigned-mirrors`
e `gitstack status` la mostra come «AVVISO catena di fornitura» (`supply_chain_warning` con
`--json`). Vedi [docs/operations.md](../docs/operations.md), sezione V5.

Mai password o URL completo: la password sta nel Secret del cluster. Con `external: true` backup, restore, upgrade e status eseguono i comandi di database nel pod `<release>-pgclient` e non nello StatefulSet del bundle; `gitstack postgres check` è il preflight (exit 11 se il database non è idoneo).

Campi sconosciuti o valori non validi sono un errore (exit 3). L'installer
mantiene la sezione `backup` esistente alla riesecuzione; si imposta con
`GITSTACK_BACKUP_DIR` e `GITSTACK_BACKUP_RETENTION`.

## Comandi

| Comando | Cosa fa | Root |
|---|---|---|
| `gitstack status [--json] [--config F]` | versione, host, salute, ultimo backup | serve per leggere il config (0600) |
| `gitstack backup [--dest D] [--key-file F] [--config F]` | archivio coerente di database, repo, allegati, Secret e configurazione | sì |
| `gitstack restore [--dest D] [--key-file F] [--config F] <archivio>` | ripristina l'archivio su un'installazione pulita della stessa versione | sì |
| `gitstack upgrade [--to V | --version vX.Y.Z] [--dest D] [--key-file F] [--timeout T] [--dry-run] [--allow-unsigned-mirrors] [--set k=v] [--values F] [--config F]` | aggiorna GitStack: controlli, backup preventivo, chart e binario, attesa della salute, rollback automatico se fallisce (D18) | sì |
| `gitstack restore --from s3 [nome] [--dest D] [--key-file F] [--config F]` | scarica da `backup.s3` l'archivio più recente (o `nome`) e lo ripristina; la chiave è quella di `backup.s3.key_file` se manca `--key-file` | sì |
| `gitstack config set backup.s3 --endpoint E --bucket B --key-file F [--prefix P] [--region R] [--retention N|bucket] [--notify-command C] [--no-check] [--remove]` | configura la copia cifrata su S3 (chiavi da `GITSTACK_S3_ACCESS_KEY`/`GITSTACK_S3_SECRET_KEY`); verifica il bucket prima di scrivere | sì |
| `gitstack config set network [--http-proxy U] [--https-proxy U] [--no-proxy L] [--registry-mirror U] [--registry-username U] [--registry-password-file F] [--download-mirror U] [--ca-bundle F] [--clear] [--force] [--config F]` | cambia proxy, mirror e CA aziendale (N9): prova a raggiungere proxy e mirror (e non cambia niente se non rispondono, salvo `--force`), rigenera `registries.yaml` e il drop-in di proxy di k3s e la CA di sistema, riavvia k3s solo se questi file cambiano e attende il nodo `Ready`, crea il ConfigMap `<release>-corporate-ca` e aggiorna il chart (`proxy.*`, `corporateCA.configMap`, `--reuse-values`), poi `network:` del config. Un valore vuoto toglie l'impostazione. Dettagli: `docs/operations.md`, «Corporate network» | sì |
| `gitstack config set host <nome> [--config F]` | cambia il nome (o IP) con cui si raggiunge GitStack: rigenera il certificato, aggiorna gli URL pubblici dei servizi (`helm upgrade --reuse-values` sul chart in `chart_dir`) e `host:` del config; avvisa se il nome non risolve e che cambiano indirizzi di clone e link nelle email | sì |
| `gitstack system-info publish|show` | pubblica (o stampa) le informazioni per la pagina Admin · System: ConfigMap `<release>-system-info` letto da core | sì |
| `gitstack postgres check --url U [--password-file F] [--ca-file F] [--json]` | preflight del Postgres esterno: versione 15–17, connessione, permessi per creare schemi e ruoli (exit 11 se non idoneo) | no |
| `gitstack version` | versione del binario | no |

I comandi che cambiano lo stato dell'host (dai prossimi item) rifiutano di
girare senza root, con exit 5 e il suggerimento `sudo`.

### `gitstack status`: da dove arriva la salute

- **Servizi**: `kubectl` (o `k3s kubectl`, se `kubectl` non è nel PATH) sul
  cluster, con `KUBECONFIG` dal config: Deployment e StatefulSet con label
  `app.kubernetes.io/part-of=gitstack` e `app.kubernetes.io/instance=<release>`
  nel namespace del config; sano se le repliche pronte ≥ desiderate.
- **API**: `GET http://<host>/api/healthz` (quello che vede un client).
- **Versione del server**: il tag dell'immagine del gateway in esecuzione
  (se il cluster non risponde, `image_tag` del config).
- **Ultimo backup**: «nessuno» se il file `backup-state.json` non esiste nella cartella di configurazione;
  altrimenti mostra data, percorso dell'archivio e (in caso di fallimento) il messaggio di errore.

### `gitstack system-info`: da dove arriva Admin · System (GIT-213)

I servizi non vedono l'host. `gitstack system-info publish` scrive il ConfigMap `<release>-system-info` (chiave `system.json`) con: database (modo, host, porta, database, sslmode, versione), backup (destinazione, ultimo esito locale e S3 con l'errore di caricamento), versioni di GitStack e k3s con l'aggiornamento disponibile, rete aziendale (proxy senza credenziali, CA). Lo lanciano da soli `install.sh`, `gitstack backup` e `gitstack upgrade`. Il chart lo monta in core (volume opzionale, `GITSTACK_SYSTEM_INFO_FILE`), che lo espone con `GET /v1/admin/system` solo all'amministratore e vi aggiunge la versione di Postgres letta ora con `SHOW server_version`. `gitstack system-info show` stampa il documento senza pubblicarlo. Dal GIT-218 il documento ha anche: `access` (host, modo HTTPS, scadenza del certificato letta dal certificato che il server presenta, porta SSH), `services` (prontezza e repliche di ogni Deployment e StatefulSet, come `gitstack status`; la data di pubblicazione è `updatedAt`), `node` (CPU, memoria e disco della macchina confrontati con il profilo N1 di `install.sh`, o «non disponibile») e `supplyChainWarning` (lo stesso avviso V5 di `gitstack status`, presente solo con `v5.allow_unsigned=true`). Sezioni che l'host non sa leggere restano «non disponibile»: mai un valore inventato. Mai segreti: né password, né chiavi S3, né credenziali del proxy.

### Codici di uscita

| Codice | Significato |
|---|---|
| 0 | riuscito (status: tutto sano) |
| 1 | status: un servizio o l'API non è sano |
| 2 | comando o opzione sconosciuti |
| 3 | configurazione assente, illeggibile o non valida |
| 4 | status: il cluster non si può interrogare (kubectl assente, API server giù) |
| 5 | serve root (anche: config non leggibile per permessi) |
| 6 | backup/restore/upgrade rifiutato con motivo (upgrade: anche downgrade e destinazione non valida) (versione diversa, archivio corrotto, cifrato senza chiave o con chiave errata) |
| 7 | upgrade fallito e tornato com'era (rollback riuscito) |
| 8 | upgrade fallito e rollback NON riuscito: serve un intervento (il messaggio dice come) |
| 9 | restore completato nei dati ma l'istanza non risponde attraverso l'Ingress entro il timeout (120 s): il messaggio dice quale URL ha fallito e con quale stato; verifica con `gitstack status` |
| 10 | backup locale riuscito ma caricamento su S3 fallito (l'archivio locale è valido; causa in `gitstack status`) |

| 11 | `gitstack postgres check`: il Postgres esterno non è idoneo (versione, connessione o permessi): il messaggio dice cosa fare |
| 70 | errore inatteso |

Errori su stderr con prefisso `ERRORE:`, come `install.sh`.

## Backup e restore (D19)

### Cosa contiene l'archivio

`gitstack backup` scrive **un solo archivio** `gitstack-backup-<data UTC>-<versione>.tar.gz`
(più `.enc` se cifrato) in `backup.destination` (default `/var/backups/gitstack`;
`--dest` lo cambia, per esempio `/srv/backup`). Cartella `0700`, archivio e
`.sha256` accanto `0600`, root-only. Dentro, `manifest.json` per primo e poi:

| Voce | Contenuto |
|---|---|
| `manifest.json` | formato, **versione** (tag immagine), **commit**, data UTC, versione del binario, release, namespace, e per ogni voce nome, dimensione e **SHA-256** |
| `database.sql` | `pg_dump` (SQL) dei soli schemi `identity` e `core` del database di GitStack, eseguito nel pod di Postgres |
| `git-data.tar` | i repo bare del volume `git-data` (modo, proprietario e date conservati) |
| `attachments.tar` | gli allegati delle issues (M-05), se il volume esiste |
| `secrets.json` | tutti i Secret con label `app.kubernetes.io/part-of=gitstack`: password di identity e admin, segreto di servizio, chiave di cifratura OIDC, chiave host SSH, configurazione OIDC, e la chiave della CA se è un Secret |
| `config/…` | i file di `/etc/gitstack` (`config.yaml` e, se esiste, la chiave della CA): la cartella si cerca, non si presuppone (la CA arriva con GIT-143) |

Il backup non scrive mai nei log nomi di utenti, repo o issue: dice solo
passi, conteggi e durate. Con `--key-file F` l'archivio è cifrato con
AES-256-GCM (chiave = HKDF-SHA256 del contenuto di `F`, almeno 16 caratteri,
per esempio `openssl rand -base64 32 > F`; il file della chiave va custodito
**fuori** dal backup). Senza la chiave un archivio cifrato non si legge. Vengono
tenuti gli ultimi `backup.retention` archivi.

Non sono inclusi: il volume di NATS/JetStream (eventi in transito, non stato)
e il Secret di Postgres (vedi restore).

### Coerenza: finestra di sola lettura (decisione del CTO)

`pg_dump` da solo è coerente per il database, ma i repo copiati dopo
potrebbero avere push più recenti del dump. Quindi **non snapshot separati**:

1. legge le repliche di `<release>-gateway` (API e UI) e `<release>-git` (push
   HTTP e SSH) e le porta a 0, aspettando che i pod siano terminati
   (`kubectl wait --for=delete`): per l'utente sono pochi secondi di 503;
2. `pg_dump` di `identity` e `core`, tar di `git-data` e degli allegati
   (letti dalla cartella dell'host del volume `local-path`);
3. riporta le repliche ai valori di prima (un componente già a 0 resta a 0);
4. solo dopo, cifra e impacchetta (il tempo di compressione non fa parte della
   finestra).

Il passo 3 avviene **sempre**: su errore, su Ctrl-C/SIGTERM (con un contesto
non annullato) e su qualsiasi uscita (`defer`); è coperto da test
(`TestBackupRestoresReplicasOnError`, `TestBackupRestoresReplicasOnInterrupt`).
Se nemmeno il ripristino riesce, l'errore lo dice e indica `kubectl scale`. Gli
errori di lettura (volumi, Secret) si scoprono *prima* di fermare qualsiasi cosa.

**Durata della finestra: non misurata.** Il comando la stampa a fine backup
(«Finestra di sola lettura»). Le misure sulla VM Hyper-V vanno fatte con l'e2e
GIT-148 e riportate qui; non ho una cifra da dichiarare. `core`, `identity` e
`web` restano in esecuzione: scrivono solo se raggiunti da gateway o git, che
sono fermi.

### Restore

```bash
# su un'installazione pulita, STESSA versione (tag immagine uguale):
sudo ./install.sh …                      # reinstalla la versione del backup
sudo gitstack restore /srv/backup/gitstack-backup-….tar.gz
sudo gitstack restore --key-file chiave /srv/backup/gitstack-backup-….tar.gz.enc
```

1. apre l'archivio e legge il manifest: se la versione è diversa da quella
   installata **rifiuta, senza toccare il cluster** (exit 6, con le due
   versioni nel messaggio); idem per archivio corrotto (SHA-256 di ogni voce),
   incompleto o cifrato senza chiave;
2. ferma gateway, git, core e identity;
3. database: in **una sola transazione** (`psql --single-transaction`) crea il
   ruolo `identity_app` se manca, rimuove gli schemi `identity` e `core` e
   carica il dump: se fallisce il database non cambia;
4. svuota e ripristina i volumi `git-data` e allegati;
5. riapplica i Secret **tranne quello di Postgres** (la sua password appartiene
   al Postgres della nuova installazione, il cui volume è nuovo; la password del
   ruolo `identity_app` la riallinea l'initContainer di identity dal Secret
   ripristinato) e rimette i file di `config/` in `/etc/gitstack`, **senza**
   sovrascrivere `config.yaml` (lo ha appena scritto l'installer);
6. **TLS e CA** (solo con `tls: internal` o `custom`; con `insecure` e
   `letsencrypt` non si fa niente): i file di `/etc/gitstack/tls` (`ca.crt`,
   `ca.key`, `server.crt`/`server.key`, `server.sans`) tornano quelli del
   backup, ma `tls/tls.conf` e `tls/install.conf` **restano quelli della nuova
   installazione**, perché descrivono la macchina di oggi (nomi e IP per i SAN,
   host). Poi, prima di riaccendere i servizi, il restore lancia
   `gitstack-tls ensure` senza opzioni: se i SAN di oggi differiscono da quelli
   del backup, o il certificato non è firmato dalla CA ripristinata, il
   certificato del server è riemesso **con la CA del backup**; altrimenti si
   riusa quello ripristinato. `ensure` aggiorna il Secret `<release>-tls`
   (Traefik lo rilegge da solo) e il ConfigMap `<release>-ca`. A servizi
   riaccesi il restore fa un `rollout restart` del Deployment `<release>-web`
   e ne aspetta il ready, perché il web monta il ConfigMap come cartella e il
   kubelet lo aggiornerebbe solo dopo circa un minuto: all'uscita
   `/downloads/ca.crt` è già la CA del backup, come `gitstack-tls fingerprint` e
   la catena servita su 443. I client che si fidavano della CA originale
   continuano a fidarsi. Se `ensure` fallisce, i dati sono già ripristinati: i
   servizi sono riaccesi, il comando esce con codice 70 e dice di rilanciare
   `sudo gitstack-tls ensure`;
7. riporta le repliche ai valori di prima e aspetta che i Deployment siano pronti;
8. **attende l'Ingress**: «Restore completato» vuol dire che l'istanza è di nuovo
   utilizzabile per intero, non solo che i pod sono Ready (Traefik può avere gli
   endpoint del web ancora vecchi e dare 502). Ogni 2 s, fino a 120 s, il
   comando chiede attraverso l'Ingress `/api/healthz` (come `status`) e, con `tls: internal` o
   `custom`, `GET http://<host>/downloads/ca.crt` (200 e un PEM `CERTIFICATE`); finisce solo
   quando entrambi danno 200 nello stesso giro. Se scade esce con **codice 9** e un messaggio
   con l'ultimo URL e stato visti: i dati sono ripristinati, si controlla con `gitstack status`.

Se un passo dopo l'arresto fallisce i servizi **restano fermi** (un database
o dei repo a metà non devono ricevere traffico) e l'errore spiega come
riprovare o riaccenderli. Le chiavi host SSH ripristinate evitano ai client
l'avviso «host key changed»; sessioni e token continuano a valere perché
identity ritrova il suo segreto di servizio e la chiave OIDC.

Limiti noti: Postgres esterno (`postgres.enabled=false`) non è supportato (il
dump passa da `kubectl exec` nel pod di Postgres); solo volumi `local-path`
(cartella sull'host).

### Procedura di prova sulla VM (per GIT-148)

Prova eseguita finora: **solo test unitari** (fake del cluster, volumi su
cartelle temporanee, nessuna VM né k3s). La procedura completa dei criteri
va fatta sulla VM `deploy/test-vm`:

1. installa la versione X; crea utenti, un'organizzazione, repo con push
   (anche via SSH), issue con allegati; annota gli SHA dei repo
   (`git ls-remote`) e tieni un clone di riferimento;
2. `sudo gitstack backup --key-file k` → verifica `ls -l` (archivio `0600`,
   cartella `0700`), `.sha256` e `manifest.json` (per un archivio non cifrato:
   `tar -xzOf … manifest.json`); durante l'esecuzione l'API deve dare 503 o
   rifiutare la connessione per pochi secondi e poi tornare 200; annota la
   «Finestra di sola lettura» stampata;
3. copia l'archivio fuori dalla VM, ripulisci la VM (disinstalla k3s e rimuovi
   `/var/lib/rancher`) e reinstalla **la stessa versione X**;
4. `sudo gitstack restore …`: login, organizzazioni, repo, issue, allegati e
   `git clone` (HTTP e SSH, senza nuovo avviso sull'host key) devono tornare
   identici ai riferimenti del punto 1;
5. reinstalla una versione diversa Y e prova il restore: deve uscire con codice 6
   e il messaggio «versione diversa», senza fermare niente.

## Sviluppo

```bash
go test ./admin/... -count=1        # dalla radice del repo
(cd admin && golangci-lint run ./... --timeout=5m)
```

I test usano un `kubectl` finto e un server HTTP di prova: non servono cluster
né root.

## Aggiornamento (D18)

`sudo gitstack upgrade [--to <versione|sha>]` porta l'installazione alla destinazione
(default: ultimo commit di `main` di `$GITSTACK_REPO`, default `fathorMB/GitStack`).
`--version vX.Y.Z` (anche `vX.Y.Z-rc.N`) è la forma stretta per un rilascio: accetta solo
tag SemVer, esclude `--to`, e scarica binario e chart `gitstack-X.Y.Z.tgz` dalla release
con quel nome (GIT-222), con la verifica delle firme (V5) attiva come per `sha-<commit>`.
L'aggiornamento di k3s è un altro compito (M-08).

1. **Controlli** (niente cambia se uno fallisce, exit 6): la destinazione esiste
   (API GitHub); **non è un downgrade** (confronto fra il commit installato e la
   destinazione: «behind» o storie diverse sono rifiutate, il downgrade non è supportato
   perché le migrazioni non si disfano); GitStack è sano adesso; il binario
   `gitstack-linux-amd64` della release `sha-<commit>` si scarica, il suo SHA-256 torna e
   la sua firma cosign (`.sigstore.json` accanto, identità del workflow `ci.yml` e issuer
   OIDC fissati, cosign pinnato dell'installer) è valida (V5); il chart si usa solo
   se il `.tgz` della release è firmato (verificato prima di estrarlo; il sorgente
   `deploy/gitstack` del commit serve per `deploy/k3s-version` e per la versione);
   ogni immagine `gitstack-*` che il chart userebbe ha una firma valida; ogni immagine che il chart
   userebbe (da `helm template` con i valori attuali, quindi anche un mirror) esiste nel
   suo registry. `--dry-run` si ferma qui.
2. **Backup preventivo** con la stessa logica di `gitstack backup` (`--dest`, `--key-file`).
3. **`helm upgrade --wait`** del chart scaricato, con i valori scelti all'installazione
   (`helm get values`, riapplicati ai default del chart nuovo: `--reuse-values` perderebbe
   le chiavi nuove) e `global.image.tag=<tag>`. Le migrazioni girano all'avvio dei servizi.
   Poi aspetta che tutti i servizi e l'API siano sani, al massimo `--timeout` (default 10 m).
4. Solo se tutto è sano: sostituzione atomica del binario (`/usr/local/bin/gitstack`), della
   copia locale del chart (`chart_dir`) e di `image_tag` nel config.

**Rollback automatico** (anche con Ctrl-C o errore a metà): ripristina binario e copia del
chart, `helm rollback` alla revisione di prima e, **se le migrazioni hanno toccato il
database** (versioni in `identity.schema_migrations` e `core.schema_migrations` cambiate o
sporche, o non leggibili), `gitstack restore` del backup appena fatto. Se il database non è
cambiato non si ripristina niente: i dati scritti nel frattempo restano. Con il restore, invece,
i dati scritti dopo il backup durante l'aggiornamento si perdono: il messaggio finale lo dice.
Alla fine il comando scrive cosa è successo e dov'è il backup; exit 7 se il rollback è
riuscito, 8 se no (con i comandi per finire a mano).

Il «sano» dell'upgrade comprende anche l'Ingress: con `tls: internal` o `custom`, dopo che i
servizi sono sani, `GET http://<host>/downloads/ca.crt` deve dare 200 con un PEM (il web
viene riavviato dall'upgrade). Entro il timeout di `--timeout` altrimenti l'upgrade conta come
salute fallita e parte il rollback di prima (niente exit nuovo).

Se una firma manca, non è valida o è di un'altra identità, l'upgrade è rifiutato (exit 6)
prima di toccare niente. Per un mirror interno non firmato c'è l'eccezione esplicita N9:
`--allow-unsigned-mirrors` (per quella volta) oppure `v5.allow_unsigned: true` nel config,
con avviso visibile; mai il default. `backup` non riavvia il
web (ferma solo gli ingressi che scrivono): non cambia.

`--set` e `--values` passano valori aggiuntivi a helm (una prova di rollback si fa, per
esempio, con un valore che impedisce a un servizio di diventare pronto).
