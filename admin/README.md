# admin/ — `gitstack`, il comando di amministrazione dell'host

`gitstack` è il binario Go con cui l'amministratore gestisce l'installazione
di GitStack **sull'host** (stato oggi; backup, restore, upgrade e config nei
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
  o è vuota; altrimenti la voce più recente. Il formato vero lo definisce
  `gitstack backup` (GIT-145).

### Codici di uscita

| Codice | Significato |
|---|---|
| 0 | riuscito (status: tutto sano) |
| 1 | status: un servizio o l'API non è sano |
| 2 | comando o opzione sconosciuti |
| 3 | configurazione assente, illeggibile o non valida |
| 4 | status: il cluster non si può interrogare (kubectl assente, API server giù) |
| 5 | serve root (anche: config non leggibile per permessi) |
| 70 | errore inatteso |

Errori su stderr con prefisso `ERRORE:`, come `install.sh`.

## Sviluppo

```bash
go test ./admin/... -count=1        # dalla radice del repo
(cd admin && golangci-lint run ./... --timeout=5m)
```

I test usano un `kubectl` finto e un server HTTP di prova: non servono cluster
né root.
