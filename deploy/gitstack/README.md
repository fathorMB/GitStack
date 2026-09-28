# Chart Helm `gitstack`

Installa su k3s tutti i componenti di M-01 con un solo `helm install`: `gateway`, `core`, `web`, PostgreSQL e NATS JetStream, con Traefik (già incluso in k3s) come ingresso. Configurazione minima (D1 [c_741b20b583f88c3c]): i valori di default bastano per un k3s a nodo singolo.

```sh
helm install gitstack deploy/gitstack
```

## Componenti e valori di default

| Componente | Kind | Note |
|---|---|---|
| `gateway` | Deployment + Service | Go, M-01/T-04. Probe `/healthz` (liveness) e `/readyz` (readiness). |
| `core` | Deployment + Service | Go, M-01/T-05. Stesse probe. Applica le proprie migrazioni Postgres all'avvio (nessun Job separato necessario per l'uso minimale di default). |
| `web` | Deployment + Service | React/SPA, M-01/T-07 — **vedi sotto**, `web.enabled`. |
| `postgres` | StatefulSet + Service headless + PVC | Bundle di default (D6 [c_4df04d65b3ac4910]); opzione DB esterno del cliente, vedi sotto. |
| `nats` | StatefulSet + Service headless + PVC | JetStream (D11 [c_74dcf9721e6f7b3e]), storage su file. |
| PVC repo Git (`gitData.enabled`, **default `false`**) | PersistentVolumeClaim | Predisposto per i futuri repository Git (D6), non un criterio di accettazione di questo item. Disattivato di default: nessun pod lo monta ancora (il servizio "git" arriva con una milestone successiva a M-01) e la storage class di default di k3s (`local-path`, `WaitForFirstConsumer`) lo lascerebbe "Pending" per sempre, bloccando `helm install --wait`. Attivalo solo insieme al servizio che lo monta. |
| Ingress | Ingress + Middleware Traefik | `/` verso `web`, `/api` verso `gateway` (con lo strip del prefisso, vedi sotto). |

## Web: `web.enabled` (GIT-7)

L'immagine di `web` (GIT-7) esiste dalla sua integrazione: `web.containerPort` (8080) e `web.probes.path` (`/healthz`) nei values combaciano con l'immagine reale (`web/Dockerfile`, `EXPOSE 8080`; `web/deploy/nginx.conf.template`, `location = /healthz`). Il Deployment imposta anche `GATEWAY_UPSTREAM` al Service k3s del gateway (`gitstack.gateway.url` in `_helpers.tpl`), sovrascrivendo il default dell'immagine (`http://gateway:8080`, che non risolverebbe: il Service si chiama `<release>-gateway`) — altrimenti il proxy `/api/*` di nginx verso il gateway non risolverebbe nel cluster.

Se serve installare senza l'immagine di `web` (es. un registry senza quel pacchetto pubblicato), disattivala esplicitamente:

```sh
helm install gitstack deploy/gitstack --set web.enabled=false
```

Con `web.enabled=false`, l'Ingress espone solo `/api` (nessuna regola `/`): è quanto fa ancora oggi l'installazione di prova in CI (vedi `.github/workflows/ci.yml`, job `chart`), che costruisce in locale solo le immagini di `gateway` e `core`. L'ambiente di sviluppo locale (`make dev-up`, GIT-10, vedi sotto) costruisce e importa anche l'immagine di `web` e la lascia attiva.

## Tag delle immagini

`gateway` e `core` sono pubblicate su ghcr.io dal job `registry` del workflow CI (`.github/workflows/ci.yml`) con due tag per ogni push a `main`/tag:

- **tag sha**: `sha-<commit-sha-completo>` (sempre, a ogni push su `main` o su un tag `vX.Y.Z`).
- **tag di versione**: pubblicato solo quando viene spinto un tag `vX.Y.Z` — non ancora avvenuto in questo repository (nessun tag esiste al 2026-09-28, GitStack è ancora in M-01, prima del primo rilascio). Non è un difetto del workflow: semplicemente nessun rilascio è stato ancora tagliato. Il job `registry` è già pronto a farlo (`on.push.tags: ['v*']` e `github.ref` che matcha `refs/tags/*`) al primo `git tag vX.Y.Z && git push --tags`.
- **Correzione proposta in questo item**: il tag di versione, quando pubblicato, riprende il nome esatto del tag Git (es. `v0.1.0`, con il prefisso `v`), mentre questo chart usa per default `Chart.AppVersion` (convenzione Helm, senza `v`, es. `0.1.0`) come tag immagine quando non specificato altrimenti. I due non combaciano: un `helm install` con i valori di default, dopo un rilascio `v0.1.0`, cercherebbe `gitstack-gateway:0.1.0`, che non esiste (esiste solo `:v0.1.0`). Per questo il workflow (`ci.yml`, job `registry`, passo "Metadati immagine") ora aggiunge anche `type=semver,pattern={{version}}` ai tag di `docker/metadata-action`, che pubblica in più il tag senza `v` (`0.1.0`), allineato ad `appVersion`.

Verificato con `docker manifest inspect` (autenticato con un token con permesso di lettura sul pacchetto) sui riferimenti pubblicati dall'ultimo run CI verde su `main` (commit `8033150...`, run [36432119351](https://github.com/fathorMB/GitStack/actions/runs/36432119351)):

- `ghcr.io/fathormb/gitstack-gateway:sha-80331500444853ff49c1021908d92351bac7636e` — manifest trovato (config `sha256:6cb2539d...`).
- `ghcr.io/fathormb/gitstack-core:sha-80331500444853ff49c1021908d92351bac7636e` — manifest trovato (config `sha256:200ccd66...`).
- Nessun tag di versione esiste ancora (nessun tag Git nel repository): non verificabile finché non viene tagliato il primo rilascio.

Per un'installazione di prova su un commit specifico, prima che esista un tag di versione:

```sh
helm install gitstack deploy/gitstack \
  --set global.image.tag=sha-<commit-sha-completo> \
  --set web.enabled=false
```

## Registry configurabile (mirror, M-08)

`global.image.registry` (default `ghcr.io`) vale per le immagini di GitStack (`gateway`, `core`, `web`). Ogni componente può sovrascriverlo singolarmente (`gateway.image.registry`, `core.image.registry`, `web.image.registry`), per un mirror parziale — utile per un'installazione air-gapped (M-08 [c_8458909a21d9035f]) che rispecchia solo alcune immagini. `postgres.image.registry` e `nats.image.registry` puntano di default a Docker Hub (immagini di terze parti, non toccate da `global.image.registry`) e sono anch'esse sovrascrivibili. Un registry privato può richiedere `global.imagePullSecrets`.

## PostgreSQL: bundle o esterno del cliente (D6)

Di default il chart installa un Postgres bundle (StatefulSet + PVC). Per un Postgres esterno del cliente:

```sh
helm install gitstack deploy/gitstack \
  --set postgres.enabled=false \
  --set postgres.external.host=<host> \
  --set postgres.external.database=<db> \
  --set postgres.external.username=<utente> \
  --set postgres.external.existingSecret=<secret-con-chiave-password>
```

Il chart valida questi campi a `helm template`/`helm install` e si ferma con un errore chiaro se mancano.

## Ingress: `/` verso web, `/api` verso gateway

`gateway` espone `/healthz`, `/readyz` e `/v1/*` (non `/api/*`, vedi `services/gateway/README.md`). L'Ingress instrada `/api` verso `gateway` tramite un Middleware Traefik (`stripPrefix`, CRD `traefik.io/v1alpha1`, gruppo Traefik v3 — su k3s con Traefik v2 usa `traefik.containo.us/v1alpha1`) che toglie il prefisso `/api` prima di raggiungere il Service: `/api/healthz` arriva al gateway come `/healthz`, `/api/v1/...` come `/v1/...`.

`ingress.host` è vuoto di default (Traefik risponde su qualsiasi host, comodo senza DNS su un'installazione a IP fisso); impostalo per restringere l'Ingress a un hostname preciso.

## Probe di liveness/readiness

Tutti i servizi Go (`gateway`, `core`) hanno probe HTTP su `/healthz` (liveness) e `/readyz` (readiness), come da convenzione dei rispettivi README. `web` ha le stesse probe su `/healthz` per coerenza (disattivabili con `web.probes.enabled: false` se GIT-7 non le implementa da subito). `postgres` e `nats` hanno probe non-HTTP (`pg_isready`, endpoint di monitor `/healthz` di NATS).

## `helm lint` e installazione di prova in CI

`.github/workflows/ci.yml`, job `chart`:

1. `helm lint deploy/gitstack`.
2. Crea un cluster effimero k3d (Traefik e le sue CRD sono già incluse, essendo `k3d` un vero k3s in Docker).
3. `helm install` con `--set web.enabled=false --set global.image.tag=sha-<sha del commit>` (l'immagine di `web` non esiste ancora, vedi sopra).
4. Attende che `gateway` e `core` siano `Ready` (`kubectl rollout status`).
5. Interroga `/healthz` tramite l'Ingress su `/api/healthz` e verifica una risposta 200.

## Ambiente di sviluppo locale (`make dev-up`, GIT-10)

Per uno sviluppo locale — cluster persistente, `web` incluso, ciclo modifica → rebuild → redeploy di un solo servizio — usa i target `make dev-*` alla radice del repo, che riusano questo stesso chart e la stessa sequenza del job `chart` sopra (k3d, build locale, `k3d image import`, `helm upgrade --install`). Guida completa: `docs/dev-environment.md`.
