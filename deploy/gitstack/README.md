# Chart Helm `gitstack`

Installa su k3s i componenti di GitStack con un solo `helm install`: `gateway`, `identity`, `core`, `web`, PostgreSQL e NATS JetStream, con Traefik (già incluso in k3s) come ingresso. Configurazione minima (D1 [c_741b20b583f88c3c]): i valori di default bastano per un k3s a nodo singolo.

```sh
helm install gitstack deploy/gitstack
```

## Componenti e valori di default

| Componente | Kind | Note |
|---|---|---|
| `gateway` | Deployment + Service | Go, M-01/T-04. Probe `/healthz` (liveness) e `/readyz` (readiness). |
| `identity` | Deployment + Service + Secret + ConfigMap | Go, M-02. Probe `/healthz` e `/readyz` (readiness verifica Postgres). Ruolo e schema Postgres dedicati, vedi sotto. Il gateway lo raggiunge con `GITSTACK_IDENTITY_URL` (calcolata dal chart). |
| `core` | Deployment + Service | Go, M-01/T-05. Stesse probe. Applica le proprie migrazioni Postgres all'avvio (nessun Job separato necessario per l'uso minimale di default). |
| `web` | Deployment + Service | React/SPA, M-01/T-07 — **vedi sotto**, `web.enabled`. |
| `postgres` | StatefulSet + Service headless + PVC | Bundle di default (D6 [c_4df04d65b3ac4910]); opzione DB esterno del cliente, vedi sotto. |
| `nats` | StatefulSet + Service headless + PVC | JetStream (D11 [c_74dcf9721e6f7b3e]), storage su file. |
| `git` (`git.enabled`, default `true`) | Deployment (Recreate) + Service + PVC `git-data` | Go, M-03. Probe `/healthz` e `/readyz`. Vedi la sezione Servizio git. |
| PVC repo Git (`gitData.enabled`, default `true`) | PersistentVolumeClaim | Montato su `/data` dal Deployment `git`. Non va attivato senza di esso: la storage class di default di k3s (`local-path`, `WaitForFirstConsumer`) lo lascerebbe `Pending` per sempre, bloccando `helm install --wait` (il chart rifiuta `git.enabled=true` con `gitData.enabled=false`). |
| Ingress | Ingress + Middleware Traefik | `/` verso `web`, `/api` verso `gateway` (con lo strip del prefisso, vedi sotto). |

## identity: ruolo e schema dedicati (GIT-36)

identity usa lo schema `identity` (D6) nello stesso database di core, con un ruolo Postgres proprio, `identity_app`, proprietario di quel solo schema e senza permessi sugli schema degli altri servizi. La password del ruolo sta in un Secret `<release>-identity-db`, generato una sola volta con `helm.sh/resource-policy: keep` (come quello di postgres: un reinstall sul volume superstite combacia col ruolo già creato); in alternativa `identity.db.existingSecret` (chiave `password`).

Admin del primo avvio (GIT-35): la password iniziale è in un Secret `<release>-identity-admin` (chiavi `username`, default `admin`, e `password`), generato una sola volta con `lookup` + `randAlphaNum` e `helm.sh/resource-policy: keep`; identity la legge dalla variabile `GITSTACK_IDENTITY_ADMIN_PASSWORD` (secretKeyRef, mai da values) e crea l'admin solo se nel database non c'è nessun amministratore. In alternativa `identity.admin.existingSecret`.

Il ruolo e lo schema li crea un initContainer del Deployment (`bootstrap-db`, immagine di postgres per `psql`) con le credenziali amministrative di Postgres (`postgres.auth` o `postgres.external`), eseguendo `files/identity-bootstrap-role.sql`. È idempotente (`CREATE ROLE` protetto da un controllo su `pg_roles`, la password si riallinea a quella del Secret) e gira a ogni avvio del pod. Le credenziali amministrative non arrivano mai al container di identity, che si connette solo come `identity_app`. Con un Postgres esterno l'utente amministrativo deve poter creare ruoli (`CREATEROLE`). `files/identity-bootstrap-role.sql` è copia identica di `services/identity/migrations/bootstrap-role.sql` (un chart non legge file fuori dalla propria cartella): il job `chart` della CI le confronta.

Variabili (`identity.env.*`): `logLevel`, `dbMaxConns`, `migrationsTimeout`, `sessionTTL`, `trustedProxies` (CIDR da cui identity si fida dell'header `X-Gitstack-Client-Ip` impostato dal gateway; default `10.42.0.0/16`, la rete dei pod di k3s: cambialo se il cluster usa un CIDR diverso). `tokenMaxLifetime` (facoltativa, `GITSTACK_IDENTITY_TOKEN_MAX_LIFETIME`: durata massima dei token personali; se vuota la variabile non è impostata e vale il default di identity). Le migrazioni le applica identity stesso all'avvio.

Segreti di servizio (F9, GIT-235): un segreto per chiamante, non uno condiviso. Il chart genera un Secret `<release>-service-secrets` con tre chiavi (`gateway`, `core`, `git`, 40 caratteri casuali ciascuna, riusate dagli upgrade con `lookup`, `helm.sh/resource-policy: keep`; in alternativa `serviceSecrets.existingSecret`, un Secret con le stesse tre chiavi) e monta ogni chiave come `GITSTACK_SERVICE_SECRET_<CHIAMANTE>` da `secretKeyRef` (mai in chiaro nei values) solo dove serve: il **gateway** riceve la sua; **identity** le tre (le verifica come Bearer su `/internal/*`, con un elenco di operazioni per chiamante); **core** la sua, quella del gateway e quella di git; **git** la sua e quella di core, mai quella del gateway. Il gateway la usa come Bearer verso `POST /internal/verify` di identity e per firmare (HMAC-SHA256) l'identità che inoltra a core; core accetta gli header `X-Gitstack-User-Id/-Username/-Scopes` con una firma valida e recente (60 s) del gateway, e da git solo per `GET /repos/{owner}/{repo}`; risponde 401 a chi li manda senza passare dal gateway (schema in `services/gateway/internal/trust`). Il Secret è generato anche con `identity.enabled=false` (core lo richiede per avviarsi). Ogni chiave può essere un elenco `nuova,vecchia` (rotazione con doppia chiave): la procedura è in `docs/operations.md`, «Key lifecycle and rotation (F9)». Il Secret `<release>-identity-service` e il valore `identity.serviceSecret.existingSecret` non esistono più: `gitstack upgrade` li toglie.

Grant al creatore (GIT-61): chi crea una risorsa con `POST /v1/resources` ne diventa `admin`. Core chiama `POST /internal/resources/{id}/grants/creator` di identity (Bearer = segreto di servizio) all'URL `GITSTACK_IDENTITY_URL`, calcolata dal chart come per il gateway; se il grant non si scrive la risorsa viene cancellata e la risposta è 503 `identity_unavailable`. Con `identity.enabled=false` core non ha `GITSTACK_IDENTITY_URL` e `POST /resources` risponde sempre 503.

Autenticazione nel gateway (GIT-54): ogni rotta di `/v1/*` ha la sua dichiarazione di sicurezza ricavata da `api/openapi.yaml` (pubblica con `security: []`, altrimenti sessione o token con gli scope di `x-required-scopes`). Senza credenziali valide il gateway risponde 401 `unauthenticated`, con un token senza scope 403 `insufficient_scope`, con identity irraggiungibile 503 `identity_unavailable` (mai fail open). Con `identity.enabled=false` il gateway non ha `GITSTACK_IDENTITY_URL`: le rotte pubbliche e i probe funzionano, ogni rotta autenticata risponde 503. Cache delle verifiche: `gateway.env.authCacheTTL` (default `30s`, esiti positivi, mai oltre la scadenza della credenziale) e `gateway.env.authCacheNegativeTTL` (default `5s`).

Gateway: `gateway.env.trustedProxies` (`GITSTACK_GATEWAY_TRUSTED_PROXIES`, default `10.42.0.0/16`, la rete dei pod di k3s dove gira Traefik) indica i proxy di cui il gateway si fida per `X-Forwarded-For` e per `X-Forwarded-Proto`.

Cookie di sessione su HTTP e HTTPS (GIT-153): `gst_session` (e il cookie di logout e di stato OIDC) è `Secure` solo se la richiesta originale è HTTPS. Traefik imposta `X-Forwarded-Proto`, il gateway lo inoltra a identity solo se il peer è in `gateway.env.trustedProxies`, e identity lo crede solo se il gateway è in `identity.env.trustedProxies` (default `10.42.0.0/16` per entrambi: se cambi il CIDR del cluster, cambia tutte e due, altrimenti su HTTPS il cookie non sarebbe `Secure`). Su HTTP in chiaro il cookie non è `Secure`, altrimenti il browser lo scarta e il login non funziona. **Rischio**: finché non c'è HTTPS (N5) il cookie di sessione viaggia in chiaro sulla LAN. Il job CI k3d lo prova con un cookie jar che rispetta `Secure` (`scripts/check-browser-login.go`).

Secondo fattore TOTP (F11, GIT-237, `identity.totp.*`): il chart genera una volta il keyring che cifra i segreti TOTP (Secret `<release>-identity-totp-key`, chiave `keys`, `GITSTACK_IDENTITY_TOTP_KEYS`) e ogni utente locale può attivarlo da Settings → Security. `identity.totp.requiredForAdmins=true` lo rende obbligatorio per gli amministratori. Rotazione e recupero: `docs/operations.md`.

Login OIDC (facoltativo, `identity.oidc.*`, GIT-39): con `identity.oidc.enabled=true` il chart imposta `GITSTACK_IDENTITY_OIDC_CONFIG_FILE`, `GITSTACK_IDENTITY_OIDC_ENC_KEY`, `GITSTACK_IDENTITY_OIDC_ENC_KEY_ID` e `GITSTACK_IDENTITY_PUBLIC_URL`. `identity.oidc.publicUrl` è obbligatorio (l'URL con cui il browser raggiunge GitStack; la redirect_uri da registrare presso il provider è `<publicUrl>/api/v1/auth/oidc/<slug>/callback`). I provider si danno in `identity.oidc.providers` (stesso formato del file di configurazione, vedi `docs/identity-oidc.md`) e finiscono in un Secret `<release>-identity-oidc` (chiave `oidc.json`) montato in `/etc/gitstack/oidc`; per tenere i client secret fuori dai values si crea il Secret a mano e si indica `identity.oidc.existingSecret`. La chiave di cifratura (32 byte in base64) la genera il chart una sola volta in `<release>-identity-oidc-key` (chiave `key`, `lookup` + `helm.sh/resource-policy: keep`) o si passa con `identity.oidc.encKey.existingSecret`; `identity.oidc.encKey.keyId` (default `k1`) va cambiato insieme alla chiave. Un cambio dei provider rilancia il pod (annotazione `checksum/oidc`). Con `enabled=false` (default) niente di tutto questo è renderizzato.

Il gateway instrada a identity i percorsi di `api/openapi.yaml` dei tag `auth`, `users`, `tokens`, `ssh-keys`, `organizations`, `teams` e `permissions` (`/v1/auth/*`, `/v1/users*`, `/v1/user/tokens*`, `/v1/user/ssh-keys*`, `/v1/orgs*`, `/v1/resources/{id}/grants*` e `/permissions`); `/v1/internal/*` non è esposto (404). Con `identity.enabled=false` il gateway parte senza `GITSTACK_IDENTITY_URL` e quelle rotte non sono montate.

## Web: `web.enabled` (GIT-7)

L'immagine di `web` (GIT-7) esiste dalla sua integrazione: `web.containerPort` (8080) e `web.probes.path` (`/healthz`) nei values combaciano con l'immagine reale (`web/Dockerfile`, `EXPOSE 8080`; `web/deploy/nginx.conf.template`, `location = /healthz`). Il Deployment imposta anche `GATEWAY_UPSTREAM` al Service k3s del gateway (`gitstack.gateway.url` in `_helpers.tpl`), sovrascrivendo il default dell'immagine (`http://gateway:8080`, che non risolverebbe: il Service si chiama `<release>-gateway`) — altrimenti il proxy `/api/*` di nginx verso il gateway non risolverebbe nel cluster.

Se serve installare senza l'immagine di `web` (es. un registry senza quel pacchetto pubblicato), disattivala esplicitamente:

```sh
helm install gitstack deploy/gitstack --set web.enabled=false
```

Con `web.enabled=false`, l'Ingress espone solo `/api` (nessuna regola `/`): è quanto fa ancora oggi l'installazione di prova in CI (vedi `.github/workflows/ci.yml`, job `chart`), che costruisce in locale solo le immagini di `gateway` e `core`. L'ambiente di sviluppo locale (`make dev-up`, GIT-10, vedi sotto) costruisce e importa anche l'immagine di `web` e la lascia attiva.

## Tag delle immagini

`gateway`, `identity` e `core` sono pubblicate su ghcr.io dal job `registry` del workflow CI (`.github/workflows/ci.yml`) con due tag per ogni push a `main`/tag:

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

Con il database esterno (N8, GIT-205) valgono anche:

| Valore | Significato |
|---|---|
| `postgres.external.port` | Porta (default 5432). |
| `postgres.external.sslMode` | `require` (default), `verify-ca`, `verify-full`, `disable`... |
| `postgres.external.caSecret` | Secret con la CA del database (chiave `ca.crt`): montato su core, identity e sul pod client, `sslrootcert` nel DSN. |
| `postgres.external.client.image.tag` | Versione del pod `<release>-pgclient` (`psql`, `pg_dump`): la versione principale deve essere quella del server (es. `15-alpine`); la imposta `install.sh`. |

Il Deployment `<release>-pgclient` (`templates/postgres/client-deployment.yaml`) esiste solo con `postgres.enabled=false`: resta acceso con `PG*` già configurati e la password dal Secret, e `gitstack backup/restore/upgrade/status` vi eseguono i comandi di database. Per preparare il database `deploy/postgres-prepare.sql`; per installare `install.sh --postgres-url` (vedi [`../README.md`](../README.md)).

## Git via HTTPS: IngressRoute diretta al servizio git (GIT-70)

Le richieste smart HTTP (`/<owner>/<repo>.git/info/refs`, `/git-upload-pack`, `/git-receive-pack`) non passano dal gateway: un `IngressRoute` Traefik (`templates/ingress-git.yaml`, `PathRegexp`, priorità alta) le manda direttamente al Service `<release>-git`, che autentica da sé (Basic auth, password = token personale, verificato da identity) e controlla i permessi. Motivo: il gateway risponde con sessioni/Bearer e errori JSON, non con `401 WWW-Authenticate: Basic` che git richiede, e un push non deve attraversare il suo proxy. `git.enabled=false` o `ingress.enabled=false` tolgono la rotta; dietro lo stesso host/TLS dell'Ingress. Il pod git riceve `GITSTACK_IDENTITY_URL` e `GITSTACK_CORE_URL`.

## Ingress: `/` verso web, `/api` verso gateway

`gateway` espone `/healthz`, `/readyz` e `/v1/*` (non `/api/*`, vedi `services/gateway/README.md`). L'Ingress instrada `/api` verso `gateway` tramite un Middleware Traefik (`stripPrefix`, CRD `traefik.io/v1alpha1`, gruppo Traefik v3 — su k3s con Traefik v2 usa `traefik.containo.us/v1alpha1`) che toglie il prefisso `/api` prima di raggiungere il Service: `/api/healthz` arriva al gateway come `/healthz`, `/api/v1/...` come `/v1/...`.

`ingress.host` è vuoto di default (Traefik risponde su qualsiasi host, comodo senza DNS su un'installazione a IP fisso); impostalo per restringere l'Ingress a un hostname preciso.

## HTTPS (N5, GIT-143)

Il chart parla HTTP di default (job k3d della CI, `helm install` a mano); l'installer (`deploy/install.sh`) accende l'HTTPS con i valori `ingress.tls.*`:

| Valore | Significato |
|---|---|
| `ingress.tls.enabled` | UI, API e git solo su `websecure` (443); senza `secretName` né `certResolver` il chart si ferma con un errore |
| `ingress.tls.secretName` | Secret `kubernetes.io/tls` creato dall'installer (`gitstack-tls`): CA interna o certificato del cliente |
| (TLSStore `default`) | con `secretName` il chart crea un `TLSStore` Traefik `default` nel namespace della release che punta a quel Secret: il certificato dell'installer vale anche per chi si collega per IP (senza SNI, Traefik altrimenti serve il suo "TRAEFIK DEFAULT CERT" perche' con ServiceLB l'indirizzo locale e' quello del pod). Non si crea con `certResolver` (`templates/tlsstore.yaml`) |
| `ingress.tls.certResolver` | resolver ACME di Traefik (Let's Encrypt), nessun Secret |
| `ingress.tls.redirect` (`true`) / `redirectPort` | su `web` (80) un `IngressRoute` + `Middleware redirectScheme` (redirect permanente: 301 per GET/HEAD, 308 per gli altri metodi (Traefik redirectScheme)) manda tutto a https (`templates/ingress-redirect.yaml`) |
| `ingress.tls.caConfigMap` | ConfigMap con `ca.crt`: il pod `web` lo monta in `/etc/gitstack/ca` e nginx lo serve su `/downloads/ca.crt` (i binari di `gs` stanno nell'immagine, vedi sotto), e la 80 serve `/downloads/ca.crt` senza redirect (serve a fidarsi di HTTPS) |

`core.env.publicUrl` e `identity.oidc.publicUrl` vanno impostati con l'URL https pubblico: l'installer lo fa. I servizi non lo usano per parlarsi (nomi dei Service). Traefik inoltra a gateway e identity con `X-Forwarded-Proto: https`. Dettagli per l'operatore: [`../../docs/tls.md`](../../docs/tls.md).

## Download di gs e skills: `/downloads` (GIT-171, G6)

L'immagine **web** contiene, costruiti in build da `scripts/build-gs-dist.sh`, i binari di `gs` (linux, darwin, windows x amd64, arm64; versione = tag dell'immagine, `sha-<commit>`), `SHA256SUMS`, `gs-skills.zip` (cartella `skills/`) e gli script di installazione: nessun accesso a internet a runtime. L'Ingress non cambia (`/` va a web, `/api` al gateway); l'nginx di web serve, in modo **pubblico** (servono per fare login):

| Percorso | Contenuto |
|---|---|
| `/downloads/gs_<os>_<arch>` (`.exe` su windows) | binario |
| `/downloads/gitstack-runner_<os>_<arch>` (`.exe` su windows) | runner esterno della CI (GIT-278, [`docs/ci-runner-esterno.md`](../../docs/ci-runner-esterno.md)) |
| `/downloads/SHA256SUMS`, `/downloads/gs-skills.zip` | checksum (di gs e del runner) e skills |
| `/downloads/index.json` | indice (versione, binari con sha256 e dimensione, skills, script, `ca_cert`) letto dalla UI |
| `/downloads/ca.crt` | certificato pubblico della CA interna (N5), solo se il ConfigMap `ingress.tls.caConfigMap` esiste; `ca_cert` in `index.json` e null altrimenti (lo decide l'entrypoint all'avvio del pod) |
| `/install-gs.sh`, `/install-gs.ps1` | script di installazione, con l'indirizzo dell'istanza scritto al posto del segnaposto `__GS_BASE_URL__` (da `X-Forwarded-Proto` e `Host`) |

`/downloads` senza slash resta alla SPA. Installazione: `curl -fsSL https://<host>/install-gs.sh | sh` (Linux, macOS) o `irm https://<host>/install-gs.ps1 | iex` (Windows). Gli script verificano lo SHA-256 contro `SHA256SUMS` dell'istanza (protegge da download corrotti, non da un'istanza ostile) e falliscono se non coincide. Con la CA interna (N5), se il certificato non è ancora fidato, scaricano `/downloads/ca.crt` in HTTP (o con `GS_INSECURE=1`, `curl -k`, solo per questo file), ne **confrontano l'impronta SHA-256** con `GS_CA_SHA256` / `-CaSha256` (quella stampata dall'installer, `sudo gitstack-tls fingerprint`; da terminale chiedono conferma) e solo dopo la usano: su Linux/macOS la tengono in `~/.config/gitstack` e la danno a git per quell'host (nel sistema con `GS_TRUST_SYSTEM=1`, sudo), su Windows la importano in `Cert:\CurrentUser\Root`. Compromesso: il primo scaricamento dello script avviene prima di fidarsi della CA, quindi con `curl -k` o con la CA già installata dall'utente; il controllo dell'impronta è quello che chiude il buco. Copia nelle release: il job CI `gs-binaries` pubblica gli stessi byte nella release `sha-<commit>`.

## ci-gateway per i runner esterni: `ci.externalGateway` (GIT-329)

Il ci-gateway di core (porta `ci.gateway.port`, 8081) è raggiungibile solo dai pod dei job interni e **non** esce dal cluster. Per far lavorare un runner esterno (macchina tua, VM, Windows, macOS: [`docs/ci-runner-esterno.md`](../../docs/ci-runner-esterno.md)) serve un'opzione, **spenta per default**:

```yaml
ci:
  enabled: true
  externalGateway:
    enabled: true
    host: ci.gitstack.lan      # obbligatorio
    # port: 8082               # porta di core per i runner esterni
    # tlsSecretName: ""        # vuoto = ingress.tls.secretName
ingress:
  tls: {enabled: true, secretName: gitstack-tls}
```

Con `ci.externalGateway.enabled=true` (e solo con `ci.enabled=true`) il chart:

- fa aprire a core una **seconda porta** (`GITSTACK_CORE_CI_EXTERNAL_ADDR`, `ci.externalGateway.port`, 8082) con lo stesso protocollo `runner.v1`, ma che in `FetchTask` accetta **solo il token di un runner registrato** (`gsr_…`). Il biglietto di un pod interno (`gsq_…`) vi riceve `Unauthenticated` e non si consuma. L'indirizzo pubblico (`GITSTACK_CORE_CI_EXTERNAL_URL`, `https://<host>`) è quello scritto nei task dei runner esterni;
- aggiunge la porta al Service di core e un ingresso nella NetworkPolicy di core **dal solo controller di ingresso** (`networkPolicy.ingressNamespace`). La NetworkPolicy dei pod dei job (`ci-job-egress`) apre solo `ci.gateway.port`: la porta esterna resta chiusa ai job;
- crea l'`IngressRoute` Traefik `<release>-ci-gateway` (`templates/ingress-ci-gateway.yaml`): `Host(<host>)`, solo `websecure`, TLS con il Secret dell'istanza (`ingress.tls.secretName`, la CA dell'istanza, o `ci.externalGateway.tlsSecretName`) o il `certResolver`, HSTS come le altre rotte. Il certificato deve coprire `<host>`: se il tuo copre solo l'host principale, emettine uno con l'host del ci-gateway nei SAN (vedi `docs/tls.md`).

Richiede `ingress.enabled`, `ingress.tls.enabled` e `ci.externalGateway.host`: senza, `helm template` si ferma con un messaggio (il token del runner non viaggia in chiaro). Con l'opzione spenta il chart non crea niente di tutto questo. Sul runner: `gitstack-runner register --url https://<host> --token gsrt_…`; il runner di Gitea usa `--instance https://<host>`. Il test del chart è in `deploy/tests/chart_hardening_test.sh` («GIT-329»).

**Routing delle etichette.** Un job con `runs-on:` che contiene un'etichetta diversa da `ubuntu-*` va ai runner esterni, non al pod interno (`runnerTypeOf`, GIT-278): `self-hosted`, `windows-…`, `macos-…`, `gpu`, ma anche `linux`. `runs-on: linux` resta quindi `queued` finché un runner esterno non ha l'etichetta `linux`; non finisce in un pod interno. Con `externalGateway` spento nessun runner esterno può raggiungere il gateway e quei job restano in coda. Dettagli in `docs/ci-runner-esterno.md`.

## Servizio git (GIT-74)

Deployment `<release>-git` (strategia `Recreate`: il PVC è `ReadWriteOnce`; `fsGroup: 10001` per l'utente dell'immagine) con il PVC `<release>-git-data` su `/data` e un Service HTTP interno `<release>-git:8080`. Il servizio espone l'API interna chiamata da core e lo smart HTTP di git (GIT-70, sotto): l'SSH (GIT-71) è attivo di default (`git.ssh.enabled`, sotto). Il servizio ha un segreto suo (`GITSTACK_SERVICE_SECRET_GIT`) e verifica quello di core (`GITSTACK_SERVICE_SECRET_CORE`), entrambi da `secretKeyRef` nel Secret `<release>-service-secrets`. Dopo ogni push accettato (HTTPS o SSH) il servizio pubblica `git.push` sul NATS interno (`GITSTACK_GIT_NATS_URL`, GIT-73, schema in `docs/events.md`); con NATS giù i push restano accettati.

| Value | Default | Significato |
|---|---|---|
| `git.enabled` | `true` | Deployment e Service di git (richiede `gitData.enabled=true`). |
| `git.image.*` | `fathormb/gitstack-git` | Come gli altri componenti (`global.image.*`). |
| `git.containerPort`, `git.service.port` | `8080` | Porta HTTP (`GITSTACK_GIT_ADDR`) e del Service. |
| `git.env.logLevel` | `info` | `GITSTACK_GIT_LOG_LEVEL`. |
| `git.resources` | 25m / 32Mi, limite 256Mi | Risorse del container. |
| `git.ssh.enabled` | `true` | Server SSH integrato del servizio git (GIT-71): crea il Service `<release>-git-ssh` (`LoadBalancer`, in k3s ServiceLB apre la porta sul nodo), la porta del container e il Secret della chiave host montato in `/etc/gitstack/ssh`. |
| `git.ssh.port` | `2222` | Porta SSH esposta (e `GITSTACK_CORE_SSH_PORT` di core). Mai la 22: l'installer non modifica l'sshd dell'host (R7). |
| `git.mirror.caConfigMap` | `""` | Mirror in push: nome di un ConfigMap con `ca.crt` (CA privata, per esempio quella interna di un secondo GitStack), montato in `/etc/gitstack/mirror-ca` con `GITSTACK_GIT_MIRROR_CA_FILE`. Sostituisce le CA di sistema per i soli push dei mirror: per spingere anche su GitHub il file deve contenere anche le CA pubbliche. Vuoto = CA di sistema. |
| `git.ssh.hostKey.existingSecret` | vuoto | Secret esistente con la chiave `ssh_host_ed25519_key`; vuoto = generata una sola volta (`<release>-git-ssh-host-key`, `lookup` + `helm.sh/resource-policy: keep`, così i client non vedono mai "host key changed"). |
| `core.env.publicUrl` | vuoto | `GITSTACK_CORE_PUBLIC_URL`, emesso solo se valorizzato. |
| `core.env.sshHost` | vuoto | `GITSTACK_CORE_SSH_HOST`, emesso solo se valorizzato. |

Core riceve `GITSTACK_GIT_URL` (Service interno di git, se `git.enabled`) e `GITSTACK_CORE_SSH_PORT` (`git.ssh.port`, oppure `off` quando `git.ssh.enabled=false`: core non pubblica allora nessun indirizzo di clone SSH).

Preflight di `install.sh`: la porta SSH (`git.ssh.port`, letta da `--set git.ssh.port=N`, altrimenti 2222) si aggiunge a 80/443/6443; se è occupata l'installer si ferma con un messaggio che dice di liberarla o di sceglierne un'altra con `--set git.ssh.port=N`. Come per le altre porte il controllo è saltato se k3s è già installato.

## Allegati di issue e commenti (GIT-107, I9)

Core tiene i file degli allegati su un PVC dedicato `<release>-attachments-data`, montato su `core.attachments.mountPath` (default `/var/lib/gitstack/attachments`, `GITSTACK_CORE_ATTACHMENTS_DIR`), con `fsGroup: 65532` per l'utente `nonroot` dell'immagine. Il volume va incluso nel backup insieme a Postgres e a `git-data` (D19).

**Limite noto (decisione del CEO): con il volume `ReadWriteOnce` degli allegati, core gira a una sola replica con strategia `Recreate`.** Il chart rifiuta `core.replicaCount > 1` con gli allegati attivi e imposta `strategy: Recreate` sul Deployment di core. Conseguenza: ogni aggiornamento o riavvio di core ha una breve interruzione (il nuovo pod parte solo quando il vecchio ha rilasciato il volume), e core non scala in orizzontale finché gli allegati stanno su un volume RWO. Con `core.attachments.enabled=false` il volume non esiste, core torna a `RollingUpdate`, e upload e download di allegati rispondono 503.

| Value | Default | Significato |
|---|---|---|
| `core.attachments.enabled` | `true` | PVC, montaggio, `Recreate` e variabili d'ambiente degli allegati. |
| `core.attachments.mountPath` | `/var/lib/gitstack/attachments` | `GITSTACK_CORE_ATTACHMENTS_DIR`. |
| `core.attachments.repoQuotaBytes` / `core.attachments.userQuotaBytes` | `1073741824` / `536870912` | Quota di allegati per repo (1 GiB) e per utente su tutti i repo (512 MiB), `GITSTACK_CORE_ATTACHMENTS_REPO_QUOTA_BYTES` / `_USER_QUOTA_BYTES` (F17): oltre, 413 `attachment_quota_exceeded`. `0` = nessun limite. |
| `core.attachments.maxBytes` | `10485760` | Limite per file (10 MiB), `GITSTACK_CORE_ATTACHMENTS_MAX_BYTES`: oltre, 413 `attachment_too_large`. Lo stesso valore è passato al gateway (`GITSTACK_GATEWAY_ATTACHMENTS_MAX_BYTES`), che lo applica al corpo dell'upload più 64 KiB di overhead multipart. |
| `core.attachments.orphanTtl` | `24h` | Un allegato mai collegato a una issue o a un commento si elimina dopo questo tempo (`GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL`). |
| `core.attachments.persistence.size`, `.storageClassName`, `.accessMode` | `5Gi`, vuoto, `ReadWriteOnce` | Il PVC. |
| `core.webhookSecret.keyId` | `k1` | Id della chiave con cui core cifra i segreti dei webhook (`GITSTACK_WEBHOOK_SECRET_KEY_ID`); la chiave sta nel Secret `<release>-core-webhook-key` (chiave `key`, generata una volta, `helm.sh/resource-policy: keep`). Per ruotarla: nuova `key` e nuovo `keyId`, la vecchia in `oldKeys` (`k1:<chiave>`). |
| `core.webhookSecret.existingSecret` | vuoto | Alternativa: Secret esistente con `key` (e, facoltativa, `oldKeys`). |
| `egress.allow`, `egress.deny`, `egress.clusterCIDRs` | vuoti, `10.42.0.0/16` e `10.43.0.0/16` | Protezione SSRF delle consegne dei webhook (C8, `pkg/egress`): diventano `GITSTACK_EGRESS_ALLOW`, `_DENY` e `_CLUSTER_CIDRS` di core. |
| `proxy.http`, `proxy.https`, `proxy.noProxy` | vuoti | Proxy aziendale (N9): diventano `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY` di core (webhook, SMTP), git (mirror) e identity (OIDC). `NO_PROXY` include sempre localhost, i Service di GitStack, `.svc`, `.cluster.local` e `egress.clusterCIDRs`. La policy `egress.*` resta applicata alla destinazione vera, prima del proxy. Li imposta `install.sh` da `--http-proxy`, `--https-proxy` e `--no-proxy` (N9a). |
| `corporateCA.configMap` | vuoto | ConfigMap con la chiave `ca.crt` (CA aziendale, `--ca-bundle`): montata in core, git e identity (`/etc/gitstack/corporate-ca/ca.crt`) e aggiunta alle CA di sistema dei servizi Go con `SSL_CERT_DIR`. Per git (mirror) è `GITSTACK_GIT_MIRROR_CA_FILE` e sostituisce le CA di sistema, salvo `git.mirror.caConfigMap`. |

Il gateway taglia ogni altro corpo di richiesta a 1 MiB (`GITSTACK_GATEWAY_MAX_BODY_BYTES`, non esposto nei values; core applica lo stesso tetto): oltre, 413 `body_too_large`. Il tetto degli allegati segue `core.attachments.maxBytes`, quindi alzarlo non richiede altro lato gateway.

Il gateway applica `gateway.env.coreTimeout` (default `30s`, prima `5s`) all'intera richiesta verso core, upload compreso: 10 MB in 30 secondi richiedono circa 3 Mbit/s. Se alzi `core.attachments.maxBytes` alza anche il timeout, e `client_max_body_size`-simili di un proxy davanti a Traefik, se ce n'è uno.

## Email delle notifiche: SMTP facoltativo (GIT-134, C5)

L'SMTP è **facoltativo**. Con la sezione `smtp` vuota (default) GitStack manda solo notifiche in-app: nessun errore, nessun tentativo di invio, e la UI nasconde le opzioni email (`GET /user/notification-preferences` risponde `emailAvailable: false`). Con `smtp.host` valorizzato il chart crea il Secret `<release>-smtp` (chiavi `host`, `port`, `security`, `username`, `password`, `from`) e core lo legge come `GITSTACK_CORE_SMTP_*`; un cambio dei valori riavvia core (annotazione `checksum/smtp`).

| Value | Default | Significato |
|---|---|---|
| `smtp.host` | vuoto | Server SMTP (`GITSTACK_CORE_SMTP_HOST`). Vuoto = email disattivate. |
| `smtp.port` | `0` | `GITSTACK_CORE_SMTP_PORT`; `0` = la porta standard della protezione (`starttls` 587, `tls` 465, `none` 25). |
| `smtp.security` | `starttls` | `starttls` (STARTTLS obbligatorio), `tls` (TLS implicito) o `none` (solo reti fidate: con `none` le credenziali non partono verso un host non locale). `GITSTACK_CORE_SMTP_SECURITY`. |
| `smtp.username`, `smtp.password` | vuoti | Autenticazione PLAIN (`GITSTACK_CORE_SMTP_USER`, `GITSTACK_CORE_SMTP_PASSWORD`); vanno impostati insieme o per niente. |
| `smtp.from` | vuoto | Mittente, es. `GitStack <noreply@example.com>` (`GITSTACK_CORE_SMTP_FROM`). Obbligatorio con `smtp.host`: senza, il chart si ferma. |
| `smtp.existingSecret` | vuoto | Un Secret tuo con le stesse chiavi (obbligatorie `host` e `from`) invece dei values, per non tenere la password nel file dei values. |

```sh
helm upgrade --install gitstack deploy/gitstack \
  --set smtp.host=smtp.example.com --set smtp.from='GitStack <noreply@example.com>' \
  --set smtp.username=robot --set-string smtp.password="$SMTP_PASSWORD"
```

Come si comportano le email (regola C5):

- **Per tipo.** Ogni utente sceglie, per ciascun motivo di notifica (`PUT /user/notification-preferences`), se riceve anche l'email. Default: sì per menzioni (`mentioned`) e assegnazioni (`assigned`), no per gli altri tipi.
- **Raggruppamento: finestra di 10 secondi per issue.** La prima notifica per email di un utente su una issue fissa l'invio a 10 secondi dopo; quelle che arrivano nel frattempo per la stessa issue partono nella stessa email. Passata la finestra, la successiva apre una nuova email. La finestra è fissa (`notify.DefaultEmailWindow`), non un value del chart. Se l'utente legge o archivia la notifica prima dell'invio, l'email non parte.
- **Link, nessuna risposta.** L'email contiene il link alla issue (`core.env.publicUrl` + `/<owner>/<repo>/issues/<n>`, quindi imposta `core.env.publicUrl`), `Auto-Submitted: auto-generated` e nessun `Reply-To`: non si risponde via email.
- **Mai agli agenti.** Gli utenti `agent` ricevono le notifiche solo nella casella in-app (C4); le loro preferenze si salvano ma `emailAvailable` è `false`.
- **Errori SMTP.** Un invio fallito si ritenta con attesa crescente (1 minuto, poi 2) per al massimo 3 tentativi in tutto; poi la notifica resta solo in-app e core scrive un log di errore (`email: invio non riuscito, tentativi esauriti`). Gli invii girano in un processo separato dal motore delle notifiche: un SMTP lento o giù non rallenta né blocca le notifiche in-app.
- **Più repliche.** Un advisory lock su Postgres fa lavorare un solo dispatcher alla volta: nessuna email doppia.

## Firme del registry

Con `git.registry.enabled=true`, il chart crea e conserva il Secret `<release>-registry-signing-key` (chiave `cosign.key`, ECDSA P-256) usato solo dal servizio git per firmare le immagini pubblicate dai job CI. Per fornire una chiave esistente imposta `git.registry.signing.existingSecret`; il Secret deve contenere `cosign.key` in PEM. La chiave pubblica autenticata è disponibile da `GET /api/v1/registry/signing-key` e serve a `cosign verify --key cosign.pub <immagine>`.

### Rotazione

La rotazione è manuale: sostituisci il Secret, riavvia il Deployment git e conserva la chiave pubblica precedente per verificare le firme già emesse. Non esiste rotazione automatica.

## Probe di liveness/readiness

Tutti i servizi Go (`gateway`, `identity`, `core`, `git`) hanno probe HTTP su `/healthz` (liveness) e `/readyz` (readiness), come da convenzione dei rispettivi README. `web` ha le stesse probe su `/healthz` per coerenza (disattivabili con `web.probes.enabled: false` se GIT-7 non le implementa da subito). `postgres` e `nats` hanno probe non-HTTP (`pg_isready`, endpoint di monitor `/healthz` di NATS).

## `helm lint` e installazione di prova in CI

`.github/workflows/ci.yml`, job `chart`:

1. `helm lint deploy/gitstack`.
2. Crea un cluster effimero k3d (Traefik e le sue CRD sono già incluse, essendo `k3d` un vero k3s in Docker) con `k3d cluster create --wait`.
3. Attende (con timeout esplicito, GIT-21) che l'API server del cluster risponda in modo stabile (`kubectl get --raw=/readyz` in loop, poi `kubectl wait --for=condition=Ready node --all`): su un runner CI il cluster appena creato può restare intermittentemente irraggiungibile per qualche secondo dopo che `k3d cluster create --wait` è tornato.
4. Attende (con timeout esplicito, GIT-21) che la CRD `middlewares.traefik.io` sia presente e `Established` (polling jsonpath su `.status.conditions`, GIT-50: `kubectl wait` esce subito su una CRD appena creata): su k3d, Traefik e le sue CRD vengono installate in modo asincrono dall'helm-controller di k3s, e un `helm install` troppo anticipato del chart (che usa un Middleware Traefik, vedi sopra) fallirebbe con un errore criptico ("no matches for kind Middleware").
5. `helm install` (gateway, identity, core e git costruiti in locale) con `--set web.enabled=false --set global.image.tag=sha-<sha del commit>` (l'immagine di `web` non esiste ancora, vedi sopra).
6. Attende che `gateway`, `identity`, `core` e `git` siano `Ready` (`kubectl rollout status`, non `kubectl wait`: nota di revisione di GIT-8, allineata qui).
7. Interroga `/healthz` tramite l'Ingress su `/api/healthz` e verifica una risposta 200.
8. Verifica identity dietro il gateway: `GET /api/v1/auth/session` senza cookie risponde 401 `unauthenticated` e `POST /api/v1/internal/verify` risponde 404. Prima di tutto, `diff` fra le due copie del bootstrap SQL.

## Ambiente di sviluppo locale (`make dev-up`, GIT-10)

Per uno sviluppo locale — cluster persistente, `web` incluso, ciclo modifica → rebuild → redeploy di un solo servizio — usa i target `make dev-*` alla radice del repo, che riusano questo stesso chart e la stessa sequenza del job `chart` sopra (k3d, build locale, `k3d image import`, `helm upgrade --install`). Guida completa: `docs/dev-environment.md`.

## Isolamento di rete, NATS e securityContext (F8, GIT-234)

- **NetworkPolicy** (`networkPolicy.enabled`, default `true`): default-deny su ingresso e uscita di tutti i pod del rilascio, più allow espliciti: il namespace del controller di ingresso (`networkPolicy.ingressNamespace`, default `kube-system`) verso web, gateway e git HTTP; SSH verso git da qualunque sorgente; web → gateway; gateway → core e identity; core e git → identity; core ↔ git; core e identity → Postgres; core e git → NATS; DNS verso kube-system. Core, identity e git escono anche verso fuori (webhook, OIDC, mirror) a meno dei CIDR del cluster (`networkPolicy.allowInternetEgress`); la destinazione la giudica `pkg/egress`. k3s le applica con il suo controller di rete. Con un Postgres esterno l'uscita è aperta sulla sola porta del database.
- **NATS con credenziali**: il server parte con `--user`/`--pass` dal Secret `<release>-nats-auth` (chiavi `user` e `password`, generato una sola volta, `helm.sh/resource-policy: keep`; alternativa `nats.auth.existingSecret`). core e git lo ricevono come `NATS_USER`/`NATS_PASSWORD` e lo mettono nell'URL NATS. Un pod diverso da core e git non raggiunge la porta; chi la raggiunge non entra senza password.
- **securityContext**: ogni container toglie tutte le capability e vieta l'escalation; ogni pod usa il profilo seccomp `RuntimeDefault`. Gateway, core, identity e NATS hanno il filesystem radice in sola lettura (`emptyDir` su `/tmp`); web, git, Postgres e il client Postgres no (scrivono all'avvio). L'entrypoint di Postgres tiene `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETGID` e `SETUID`.
- **Residuo**: niente TLS dentro il cluster (HTTP fra i servizi, NATS e Postgres incluso in chiaro): vedi F8 in `docs/security/asvs-l2.md`.
- Un pod di prova che deve parlare con NATS (come fa `deploy/test-vm/e2e/remote.sh`) prende le label di core **e** `app.kubernetes.io/part-of=gitstack` (senza quest'ultima la policy `allow-dns` non lo seleziona e il DNS è chiuso) e le credenziali dal Secret.
- Test: `deploy/tests/chart_hardening_test.sh` (rendering con `helm template`).

## Hosting delle app: `apps.enabled` (GIT-375, ADR 0002)

Spento di default. `apps.enabled=true` richiede `apps.domain` (il dominio delle app) e imposta su core
`GITSTACK_CORE_APPS_ENABLED`, `GITSTACK_CORE_APPS_DOMAIN`, `GITSTACK_CORE_APPS_AUTH_MIDDLEWARE`
(da `apps.authMiddleware`, facoltativo) e, per il RoleBinding dei namespace, `GITSTACK_CORE_APPS_CONTROLLER_SA`
e `GITSTACK_CORE_APPS_WORKLOAD_ROLE`. `templates/apps-rbac.yaml` crea il ClusterRole del controller
(`namespaces`, `rolebindings` create/get, `bind` sul solo ruolo modello) e il ClusterRole modello
`<release>-app-workload`, che **non ha ClusterRoleBinding**: lo lega solo il RoleBinding che core crea in
ogni namespace `gitstack-app-<id>`. Con `ci.enabled` core usa `ci-dispatcher`, altrimenti `<release>-core`.
Con `apps.enabled=false` (e `apps.domain` vuoto) non si rende nessuna risorsa delle app. Il test è in
`deploy/tests/chart_hardening_test.sh`. Come accenderlo: [deploy/README.md](../README.md#hosting-delle-app-accenderlo-git-375-adr-0002).
