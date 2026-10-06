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
```

Campi sconosciuti o valori non validi sono un errore (exit 3). L'installer
mantiene la sezione `backup` esistente alla riesecuzione; si imposta con
`GITSTACK_BACKUP_DIR` e `GITSTACK_BACKUP_RETENTION`.

## Comandi

| Comando | Cosa fa | Root |
|---|---|---|
| `gitstack status [--json] [--config F]` | versione, host, salute, ultimo backup | serve per leggere il config (0600) |
| `gitstack backup [--dest D] [--key-file F] [--config F]` | archivio coerente di database, repo, allegati, Secret e configurazione | sì |
| `gitstack restore [--dest D] [--key-file F] [--config F] <archivio>` | ripristina l'archivio su un'installazione pulita della stessa versione | sì |
| `gitstack config set host <nome> [--config F]` | cambia il nome (o IP) con cui si raggiunge GitStack: rigenera il certificato, aggiorna gli URL pubblici dei servizi (`helm upgrade --reuse-values` sul chart in `chart_dir`) e `host:` del config; avvisa se il nome non risolve e che cambiano indirizzi di clone e link nelle email | sì |
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
- **Ultimo backup**: «nessuno» se la cartella `backup.destination` non esiste
  o è vuota; altrimenti la voce più recente (i `.sha256` non contano).

### Codici di uscita

| Codice | Significato |
|---|---|
| 0 | riuscito (status: tutto sano) |
| 1 | status: un servizio o l'API non è sano |
| 2 | comando o opzione sconosciuti |
| 3 | configurazione assente, illeggibile o non valida |
| 4 | status: il cluster non si può interrogare (kubectl assente, API server giù) |
| 5 | serve root (anche: config non leggibile per permessi) |
| 6 | backup/restore rifiutato con motivo (versione diversa, archivio corrotto, cifrato senza chiave o con chiave errata) |
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
6. riporta le repliche ai valori di prima e aspetta che i Deployment siano pronti.

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
