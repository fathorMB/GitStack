# Ambiente di sviluppo locale (M-01/T-10, GIT-10)

Riproduce il cluster GitStack in locale con un comando, per provare le modifiche senza una VM. Riusa lo stesso chart Helm (`deploy/gitstack`, GIT-8) e la stessa sequenza del job `chart` di `.github/workflows/ci.yml`: cluster [k3d](https://k3d.io/) (un [k3s](https://k3s.io/) vero in Docker) → build locale delle immagini → `k3d image import` → `helm upgrade --install`. Nessun chart o manifest paralleli.

I comandi sono target di un `Makefile` alla radice del repo: `make dev-up`, `make dev-down`, `make dev-redeploy SVC=<servizio>`, `make dev-status`.

## Prerequisiti

Il `Makefile` gira in **bash**: su Linux e macOS è la shell di sistema; su **Windows serve Git Bash o WSL2** (non `cmd.exe`/PowerShell — `make` in PowerShell non esegue le regole in bash).

Servono, sul `PATH` di quella bash:

- **Docker** con un demone raggiungibile (`docker info` deve rispondere), usato per costruire le immagini e da k3d per far girare i nodi del cluster come container.
  - Linux: Docker Engine.
  - macOS: [Docker Desktop](https://www.docker.com/products/docker-desktop/) o un'alternativa compatibile (es. Colima).
  - Windows: [Docker Desktop](https://www.docker.com/products/docker-desktop/) con il backend WSL2 attivo (Settings → General → "Use the WSL 2 based engine", e l'integrazione abilitata sulla distribuzione WSL usata per lo sviluppo).
- **kubectl** — client Kubernetes, per verificare i rollout e ispezionare il cluster.
- **helm** (>= 3.14; verificato con v3.16.3, stessa versione del job `chart` di CI) — installa/aggiorna il chart.
- **k3d** — `make dev-up`/`dev-down` lo installano da soli (versione pinnata, vedi sotto) se non è già sul `PATH`, con lo stesso installer usato dal job `chart` di CI (`curl ... k3d-io/k3d/main/install.sh`), ma con `TAG` fissato invece di prendere l'ultima versione. Se preferisci installarlo tu prima, usa la stessa versione (vedi `K3D_VERSION` nel `Makefile`) per evitare disallineamenti.

`make` stesso: preinstallato su Linux e macOS (o via i pacchetti di sviluppo standard, es. `xcode-select --install` su macOS); su Windows via Git Bash (il pacchetto "make" non è incluso di default in Git for Windows: installalo con `pacman -S make` dal prompt MSYS2 di Git Bash, oppure gira dentro WSL2 dove `sudo apt install make` basta) o dentro WSL2 (`sudo apt install make`).

### Windows/WSL2, nota sui percorsi

Se lavori da Git Bash su Windows con il repo su una cartella Windows nativa (es. `E:\...`), Docker Desktop con backend WSL2 vede comunque i container: `docker build`/`k3d` funzionano da Git Bash. Se invece lavori dentro una distribuzione WSL2, tienti il repo sotto il filesystem Linux (`~/...`) o su un mount `/mnt/<lettera>/...`: entrambi funzionano, ma un repo sotto `/mnt/...` è più lento su operazioni con molti file piccoli (`node_modules`, moduli Go) — preferibile clonare dentro WSL2 stesso per lo sviluppo quotidiano.

## Un comando per crearlo, uno per distruggerlo

```sh
make dev-up      # crea il cluster k3d (se manca), costruisce le immagini di gateway/identity/core/web
                  # dai Dockerfile del repo, le importa nel cluster e fa
                  # `helm upgrade --install` del chart deploy/gitstack (web incluso, GIT-7)
make dev-down     # distrugge il cluster k3d
```

`identity` e `git` non hanno ancora un'immagine (arrivano con milestone successive a M-01, vedi `deploy/README.md`): non sono nel chart, quindi non fanno parte di `make dev-up`, come già per il job `registry`/`chart` di CI.

Dopo `make dev-up`, l'Ingress Traefik del cluster è raggiungibile su `http://localhost:8080` (stessa mappatura di porta del job `chart` di CI, `--port "8080:80@loadbalancer"`):

```sh
curl http://localhost:8080/api/healthz   # -> {"status":"ok","version":"0.1.0"} (gateway)
curl http://localhost:8080/              # -> web UI (index.html)
```

`make dev-down` seguito da `make dev-up` ricrea l'ambiente da zero in pochi minuti (misurato: vedi il riepilogo di GIT-10 nel item tracker per i tempi effettivi di un'esecuzione).

## Versioni pinnate

`k3d` e l'immagine `k3s` usata dal cluster sono pinnate nel `Makefile` (variabili `K3D_VERSION`, `K3S_IMAGE`), non lasciate "all'ultima versione disponibile": il job `chart` di CI non passa `--image` a `k3d cluster create`, quindi la versione di k3s che risolve a runtime (dal canale di aggiornamento di k3s) può cambiare da un run all'altro. Per la riproducibilità richiesta da questo ambiente, `make dev-up` fissa `k3d` (variabile `K3D_VERSION`) e passa esplicitamente `--image` con un tag `k3s` pinnato (`K3S_IMAGE`) che soddisfa il requisito del chart: **k3s >= 1.28, con Traefik v3** (gruppo CRD `traefik.io`, usato dal Middleware `stripPrefix` in `deploy/gitstack/templates/ingress.yaml` — su k3s più vecchio con Traefik v2 servirebbe `traefik.containo.us/v1alpha1`, non supportato da questo chart).

Per aggiornare le versioni pinnate, cambia `K3D_VERSION`/`K3S_IMAGE` in cima al `Makefile` (un tag `rancher/k3s:vX.Y.Z-k3sN` esistente su Docker Hub) e verifica di nuovo il ciclo `dev-down`/`dev-up`.

## Ciclo modifica → rebuild → redeploy di un solo servizio

Per non ricostruire e reinstallare tutto il chart a ogni modifica, `make dev-redeploy SVC=<servizio>` ricostruisce **una sola immagine** (`gateway`, `identity`, `core` o `web`), la reimporta nel cluster e forza il rollout del solo `Deployment` di quel servizio:

```sh
# 1. modifica il codice di un servizio, es. services/gateway/internal/httpserver/health.go
# 2. rebuild + redeploy solo di quel servizio
make dev-redeploy SVC=gateway
# 3. verifica il cambiamento (es. tramite l'Ingress)
curl http://localhost:8080/api/healthz
```

`make dev-redeploy` costruisce l'immagine con lo stesso tag della release corrente (`IMAGE_TAG`, default `dev`): senza un rollout esplicito il pod già in esecuzione continuerebbe a usare l'immagine vecchia (stesso tag, `imagePullPolicy: IfNotPresent`), quindi il target fa anche `kubectl rollout restart deployment/gitstack-<servizio>` e attende `kubectl rollout status` prima di considerarsi finito.

Il ciclo completo, con l'output effettivo di un'esecuzione, è nel riepilogo di GIT-10 (item tracker): una modifica alla versione riportata da `/api/healthz` del gateway, ricostruita e ridistribuita con `make dev-redeploy SVC=gateway`, verificata con una `curl` prima e dopo.

## `make dev-status`

Elenca i pod e l'Ingress del rilascio corrente (`kubectl get pods -o wide`, `kubectl get ingress`), utile per diagnosticare senza ricordare i nomi esatti delle risorse del chart.

## Differenze rispetto all'installazione di prova in CI

Il job `chart` di `.github/workflows/ci.yml` costruisce e installa solo `gateway`/`identity`/`core` (`--set web.enabled=false`), usa un tag immagine legato al commit (`sha-<sha>`) ed è pensato per un'esecuzione singola ed effimera in un runner. `make dev-up` costruisce e installa anche `web` (immagine disponibile da GIT-7), usa un tag fisso (`dev`) pensato per essere sovrascritto ripetutamente durante lo sviluppo, e il cluster resta in piedi tra un comando e l'altro (persistente, non effimero) finché non lanci `make dev-down`. La sequenza di fondo — creazione del cluster k3d, build locale, `k3d image import`, `helm upgrade --install` — è la stessa, per non manutenere due logiche diverse.
