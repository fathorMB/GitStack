# deploy/

Tutto ciò che serve a far girare GitStack su k3s: manifest/Helm chart interno dei servizi (`gateway`, `identity`, `git`, `core`, `web`, `postgres`, `nats`), lo script di installazione a comando singolo e la VM di test riproducibile. Architettura: `.lmbrain-lite/knowledge/architecture.md`.

- `gitstack/` — chart Helm interno (M-01/T-08): installa `gateway`, `identity`, `core`, `web`, `postgres` e `nats` (JetStream) con un solo `helm install`, Traefik come ingresso. Dettagli, valori di default e opzioni (mirror, Postgres esterno) in `gitstack/README.md`. `identity` (GIT-36) è nel chart, con ruolo e schema Postgres dedicati e le sue rotte instradate dal gateway; `git` non ha ancora un'immagine: non è nel chart.
- `install.sh` — Installer v0 (M-01/T-09, questo item): un solo comando installa k3s e GitStack (chart di `gitstack/`) su una macchina Linux pulita. Dettagli sotto.
- `test-vm/` — script che creano, ripristinano e distruggono la VM Linux locale usata per provare l'installer (M-01/T-12, GIT-12). Vedi `test-vm/README.md`.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).

## Installer v0 (`install.sh`)

Un solo comando installa k3s e GitStack su una macchina Linux pulita. Il cliente non deve sapere cos'è Kubernetes.

```sh
# Da un checkout del monorepo (usa il chart accanto allo script):
sudo ./deploy/install.sh

# Senza checkout locale (scarica da solo deploy/gitstack dal repository sorgente):
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
```

### Distribuzioni supportate

Solo **Ubuntu Server 24.04 LTS x86_64** (N2 [c_d345922b89557a3f]). Su un altro sistema (anche Ubuntu 22.04 o Pop!_OS, che arrivano con M-08 [c_8458909a21d9035f]) il preflight stampa «non supportato» e si ferma: si procede solo con `--force`, a proprio rischio. `--force` non salta i requisiti hardware. Debian, RHEL e WSL2 arrivano con M-08.

### Requisiti: due profili (N1)

Lo script li verifica in preflight prima di installare qualunque cosa (N1, confermati il 2026-10-05 [c_d345922b89557a3f]):

| Requisito | Profilo minimo: si ferma | Profilo consigliato: solo avviso |
|---|---|---|
| CPU | 4 vCPU | 8 vCPU |
| RAM | 8 GB | 16 GB |
| Disco | filesystem di almeno 60 GB **e** almeno 20 GB liberi, sul filesystem che ospita `/var/lib/rancher` (se non esiste ancora, `/`) | 200 GB su SSD (fino a circa 100 utenti) |
| Sistema | Ubuntu Server 24.04 LTS, x86_64 | |
| Accesso | root o sudo | |
| Rete | uscita verso `get.k3s.io`, `get.helm.sh` e il registry delle immagini (`ghcr.io` di default, configurabile; l'air-gapped arriva con M-08) | |
| Porte libere | 80, 443, 6443 e la porta SSH di git (già occupate da un'installazione GitStack esistente non sono un errore: vedi Idempotenza) | |

Sotto il minimo l'installer esce con exit 1 e il valore trovato; sotto il consigliato stampa `ATTENZIONE` e prosegue. L'SSD si legge da `/sys/class/block/<dev>/queue/rotational` (segue partizioni e device mapper); se non si capisce, avviso (un disco virtuale può dichiararsi rotazionale: è solo un avviso, come sulla VM di test). Lo spazio per i repo è circa il doppio della loro dimensione: sui dischi piccoli pesa il consigliato.

Tutti i GB sono decimali (10⁹ byte), non GiB: una macchina con «8 GB» di RAM riporta qualche centinaio di MiB in meno in `/proc/meminfo` (memoria riservata a firmware/hypervisor), e un disco da 60 GiB dichiara oltre 64 GB, quindi i margini di partizionamento non fanno fallire la macchina di riferimento (la VM di GIT-12). Dimensione del filesystem e spazio libero sono due controlli distinti: lo spazio libero cala dopo k3s e le immagini, e il preflight gira a ogni avvio.

Per saltare tutti i controlli (solo su una macchina già verificata a mano): `--skip-preflight` o `GITSTACK_SKIP_PREFLIGHT=1`. Non è mai attivo di default.

### Nome host (N6)

`--host NOME|IP` (ripetibile) dice con quale nome i client raggiungono GitStack: il primo è nell'URL pubblico (clone, link nelle email, OIDC), tutti finiscono nei SAN del certificato, che include sempre anche l'IP principale. Senza `--host` si usa il nome completo della macchina (`hostname -f`) se risolve a un suo indirizzo, altrimenti l'IP, con un avviso.

Il preflight verifica che il nome risolva a un indirizzo della macchina e **avvisa** (senza fermarsi) se no: nome che non risolve, che risolve altrove o solo a loopback. La verifica passa dal resolver di sistema (`getent hosts`), non da un server DNS, quindi vale anche per i nomi `.local`.

Per cambiare nome dopo l'installazione: `sudo gitstack config set host <nome>`. Rigenera il certificato (`gitstack-tls ensure`, stessa CA, SAN col nuovo nome e gli IP già presenti), aggiorna gli URL pubblici dei servizi con `helm upgrade --reuse-values` sulla copia locale del chart (`/usr/local/share/gitstack/chart`, `chart_dir` in `config.yaml`), poi `host:` di `config.yaml` e le scelte salvate per le riesecuzioni di `install.sh`. Avvisa che gli indirizzi di clone e i link nelle email già inviate puntano al nome vecchio (`git remote set-url` per i clone esistenti). Per Let's Encrypt il nome deve essere pubblico; con `--insecure-http` non c'è certificato.

#### Nomi `.local` (mDNS)

Il caso reale di homehub: il router di casa (Sky) non permette record DNS locali, quindi la macchina si chiama `homehub.local`, risolto via mDNS.

- **Sul server** serve `avahi-daemon` attivo e `libnss-mdns`: `sudo apt install avahi-daemon libnss-mdns`. Il nome è quello della macchina (`hostnamectl set-hostname homehub` → `homehub.local`). Verifica: `getent hosts homehub.local` deve dare l'IP della macchina, ed è lo stesso controllo del preflight.
  avahi risponde con gli indirizzi di tutte le interfacce, anche quelle di k3s (`cni0`): il preflight accetta un qualunque indirizzo della macchina, l'IP principale è comunque nei SAN. Per limitarlo: `allow-interfaces=<interfaccia>` in `/etc/avahi/avahi-daemon.conf`.
- **I client** devono supportare mDNS: Windows 10/11, macOS e Linux con `nss-mdns` lo fanno. Altrimenti si usa l'IP (nei SAN) o un nome nel DNS.
- **Il certificato** (CA interna) ha `homehub.local` e l'IP come SAN: `openssl x509 -in /etc/gitstack/tls/server.crt -noout -ext subjectAltName`. Va installata la CA sui client come per ogni nome.
- **I pod di k3s non risolvono `.local`** (CoreDNS non fa mDNS): niente nei servizi deve chiamare dall'interno del cluster il proprio indirizzo pubblico. Le chiamate fra servizi usano i nomi interni (`<release>-core`, ecc.); l'URL pubblico serve solo ai link e ai redirect che vede il client.
- Un nome `.local` non è accettato con `--tls letsencrypt` (non è un nome pubblico).

### k3s e Traefik v3

`INSTALL_K3S_VERSION` è pinnato a `v1.36.4+k3s1` (mai `latest`): quella versione include il chart Traefik `40.1.0` (`appVersion: v3.7.0`, cioè Traefik v3), richiesto dal Middleware `traefik.io/v1alpha1` dell'Ingress del chart (`gitstack/templates/ingress.yaml`, vedi `gitstack/README.md`). Sovrascrivibile con `INSTALL_K3S_VERSION` se serve un'altra versione, purché includa Traefik v3.

k3s installa la CRD `middlewares.traefik.io` (serve al Middleware dell'Ingress) in modo **asincrono**, tramite un HelmChart/Job interni che partono dopo che il nodo è Ready: `install.sh` aspetta esplicitamente che quella CRD compaia e diventi `Established` prima di lanciare `helm upgrade --install` del chart di GitStack, altrimenti su una macchina pulita si incontra la stessa race del job "chart" della CI (`no matches for kind Middleware`). Con la CRD già presente (esecuzioni successive) l'attesa passa subito.

### Helm

Se `helm` non è già presente, lo script scarica il tarball ufficiale pinnato a `GITSTACK_HELM_VERSION` (default `v3.16.3`, stessa versione testata in `gitstack/README.md`) da `get.helm.sh` e ne verifica il checksum `.sha256sum` pubblicato, invece dello script mobile `get-helm-3` di `helm/helm@main`: stesso principio del pin di k3s, niente scaricato da un riferimento non pinnato.

### Immagini: tag sha del commit, non "latest"

Come per k3s, mai un tag mobile. Se `--image-tag`/`GITSTACK_IMAGE_TAG` non è impostato, lo script risolve automaticamente il tag `sha-<sha commit completo>`:

- se lo script gira da un checkout Git locale (il caso tipico di `./deploy/install.sh`), usa lo sha del commit effettivamente presente su disco (`git rev-parse HEAD`);
- altrimenti (nessun checkout locale, es. `curl | sh`) risolve `GITSTACK_REPO@GITSTACK_REF` (default `fathorMB/GitStack@main`) tramite l'API di GitHub.

Un tag di versione `X.Y.Z` esisterà solo dopo il primo tag Git (vedi `gitstack/README.md`, sezione "Tag delle immagini"): non è inventato qui.

### Idempotenza

Rieseguire `install.sh` è sicuro:

- **k3s**: se è già installato (`command -v k3s`), lo script non lo reinstalla — si limita ad avviare il servizio se non è attivo. Aggiornare la versione di k3s di un'installazione esistente non è ancora supportato (M-08).
- **GitStack**: lo script usa sempre `helm upgrade --install`. Il Secret della password di Postgres ha `helm.sh/resource-policy: keep` (vedi `gitstack/README.md`): una seconda esecuzione non la cambia.
- **Porte**: il controllo preliminare sulle porte 80/443/6443 viene saltato quando k3s è già installato (sono occupate dalla stessa installazione, non da un conflitto).

### A fine installazione

Lo script stampa l'URL della UI (`https://<ip-macchina>/`, o il nome di `--host`), l'URL di salute dell'API (`/api/healthz`), le istruzioni per fidarsi della CA interna con la sua impronta SHA-256 e i comandi per verificare lo stato: `kubectl get pods`, `helm status`, `curl` sull'health endpoint, `journalctl -u k3s`.

### Comando `gitstack` e configurazione

L'installer scrive `/etc/gitstack/config.yaml` (root-only, `0600`) e installa in `/usr/local/bin/gitstack` il comando di amministrazione (`admin/`, GIT-142) con il checksum SHA-256 verificato (`--admin-binary`, `--admin-sha256`; se non c'è un binario pubblicato lo salta con un avviso). Dopo l'installazione: `sudo gitstack status`. Formato del file, build e codici di uscita: [`../admin/README.md`](../admin/README.md).

### Sicurezza: HTTPS, CA interna, utente admin

**HTTPS (N5, GIT-143).** Senza opzioni l'installer crea una CA interna (chiave in `/etc/gitstack/tls/ca.key`, solo root, mai in Kubernetes) e un certificato per il nome dell'host e il suo IP; Traefik serve la 443 e reindirizza la 80. Il certificato della CA si scarica da `/downloads/ca.crt`. Opzioni: `--host NOME|IP` (SAN e URL pubblico, es. `homehub.local`), `--tls letsencrypt` (+ `--tls-email`), `--tls-cert`/`--tls-key` (certificato del cliente), `--insecure-http` (solo prove locali, con avviso nella UI). Il rinnovo del certificato interno è automatico (`gitstack-tls-renew.timer`, ogni notte, a meno di 30 giorni dalla scadenza). Procedura per fidarsi della CA su Linux, macOS e Windows, rinnovo, Let's Encrypt: [`../docs/tls.md`](../docs/tls.md). Lo script `gitstack-tls.sh` (installato in `/usr/local/sbin/gitstack-tls`) fa CA, emissione e rinnovo.

**Utente admin (GIT-35).** Al primo avvio, su un database senza nessun amministratore, `identity` crea l'utente `admin`. La password iniziale la genera il chart una sola volta nel Secret `<release>-identity-admin` (`helm.sh/resource-policy: keep`): l'installer non la stampa mai, mostra solo il comando per leggerla:

```sh
k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

Al primo login la password va cambiata: finché non lo fai ogni altra chiamata risponde 403 `password_change_required`. Rieseguire `install.sh` non cambia né il Secret né l'admin.

### Opzioni principali

`install.sh --help` le elenca tutte. Le più usate: `--force`, `--skip-preflight`, `--release-name`, `--namespace`, `--image-tag`, `--image-registry` (mirror, M-08), `--host`, `--tls`, `--tls-cert`/`--tls-key`, `--insecure-http` (HTTPS, vedi sopra), `--values`/`--set` (valori Helm aggiuntivi, es. `postgres.enabled=false` per un Postgres esterno del cliente — vedi `gitstack/README.md`).

### Verifica in CI

`.github/workflows/ci.yml`, job `shellcheck`: `shellcheck deploy/install.sh deploy/gitstack-tls.sh deploy/tests/preflight_test.sh` (e gli altri script di shell del repository), poi `deploy/tests/preflight_test.sh`: prova i profili di N1, il sistema non supportato e la verifica del nome senza root né VM (le letture di CPU, RAM, disco, DNS sono funzioni che il test ridefinisce). Non verifica un'installazione reale (serve un sistema con systemd, non disponibile nei runner container-based): quella si fa sulla VM di `test-vm/` (GIT-12) o su una VM/container Ubuntu 24.04 usa e getta, vedi il riepilogo dell'item GIT-9.
