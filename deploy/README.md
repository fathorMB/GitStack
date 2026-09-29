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

Solo **Ubuntu Server 24.04 LTS x86_64** in M-01. Debian e RHEL arrivano con M-08/T-01 [c_8458909a21d9035f]. WSL2 (Windows) arriva anch'esso con M-08.

### Requisiti minimi

Soglia provvisoria (decisione di Atlas), da rivedere con M-08 [c_8458909a21d9035f]. Lo script li verifica in preflight prima di installare qualunque cosa, e si ferma con un messaggio chiaro (valore trovato / minimo richiesto) se non sono rispettati:

| Requisito | Minimo |
|---|---|
| Sistema operativo | Ubuntu Server 24.04 LTS, architettura x86_64 |
| CPU | 4 vCPU |
| RAM | 8 GB |
| Disco | macchina con almeno 60 GB di disco (sul filesystem che ospiterà i dati di k3s: `/var/lib/rancher` se esiste già, altrimenti `/`); il preflight verifica in concreto ≥55 GB di filesystem e ≥20 GB liberi — vedi sotto |
| Accesso | root o sudo |
| Rete | uscita verso `get.k3s.io`, `get.helm.sh` e il registry delle immagini (`ghcr.io` di default, configurabile — non è un vincolo air-gapped, quello arriva con M-08) |
| Porte libere | 80, 443, 6443 (già occupate da un'installazione GitStack esistente non sono un errore: vedi Idempotenza) |

I controlli RAM usano GB decimali (10⁹ byte), non GiB: una macchina con "8 GB" di RAM nominali riporta spesso qualche centinaio di MiB in meno in `/proc/meminfo` per memoria riservata a firmware/hypervisor — con i GiB il controllo fallirebbe quasi sempre anche su una macchina conforme.

Per il disco il preflight non richiede "60 GB liberi": su un disco *da* 60 GB (il profilo minimo, uguale al disco della VM di GIT-12), dopo il sistema operativo lo spazio libero è per costruzione sotto i 60 GB, e cala ulteriormente dopo k3s e le immagini di GitStack — anche alle esecuzioni successive, perché il preflight gira a ogni avvio dello script. Controlla quindi due soglie più realistiche, entrambe in GB decimali: la dimensione del filesystem (**≥55 GB**, con margine per l'overhead di partizionamento/boot rispetto ai 60 GB nominali) e lo spazio libero (**≥20 GB**, sufficiente per k3s, le immagini di GitStack e i volumi di Postgres/NATS). Verificato simulando in un container Ubuntu 24.04 un disco ext4 da 60 GiB (come quello di GIT-12): il filesystem risultante è ~63 GB decimali con ~60 GB liberi appena formattato, ben sopra entrambe le soglie.

Per saltare i controlli (solo su una macchina già verificata a mano): `--skip-preflight` o `GITSTACK_SKIP_PREFLIGHT=1`. Non è mai attivo di default.

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

Lo script stampa l'URL della UI (`http://<ip-macchina>/`), l'URL di salute dell'API (`http://<ip-macchina>/api/healthz`) e i comandi per verificare lo stato: `kubectl get pods`, `helm status`, `curl` sull'health endpoint, `journalctl -u k3s`.

### Sicurezza: CA interna, certificati, utente admin

Non ancora implementati in v0: l'installazione parla HTTP in chiaro sull'IP della macchina e non crea ancora nessun utente admin: il servizio `identity` è nel chart (GIT-36), ma l'admin al primo avvio arriva con GIT-35. Completamento previsto in M-02/M-08 [c_8458909a21d9035f].

### Opzioni principali

`install.sh --help` le elenca tutte. Le più usate: `--skip-preflight`, `--release-name`, `--namespace`, `--image-tag`, `--image-registry` (mirror, M-08), `--values`/`--set` (valori Helm aggiuntivi, es. `postgres.enabled=false` per un Postgres esterno del cliente — vedi `gitstack/README.md`).

### Verifica in CI

`.github/workflows/ci.yml`, job `shellcheck`: `shellcheck deploy/install.sh` (e gli altri script di shell del repository). Non verifica un'installazione reale (serve un sistema con systemd, non disponibile nei runner container-based): quella si fa sulla VM di `test-vm/` (GIT-12) o su una VM/container Ubuntu 24.04 usa e getta, vedi il riepilogo dell'item GIT-9.
