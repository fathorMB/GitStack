# core

API di repo (metadati), issues, commenti, etichette, milestone, notifiche, webhook; consuma eventi dal bus NATS (es. `fixes #12` chiude la issue). API complete arrivano con le milestone successive a M-01/T-05.

## M-01/T-05 (questo task)

In M-01, core implementa solo la risorsa di prova del contratto (`Resource`, schema generico D15 [c_4ef209e79c5b2ddb]), su uno schema Postgres dedicato ("core", D6 [c_4df04d65b3ac4910]): CRUD completo, migrazioni versionate, health/readiness che verificano il database. Nessuna tabella condivisa con altri servizi: identity, git e core (quando arriveranno le loro migrazioni) hanno ciascuno il proprio schema.

- `GET /healthz`, `GET /readyz`: probe di liveness/readiness (convenzione k8s), non fanno parte del contratto pubblico `api/openapi.yaml`. `/healthz` risponde sempre `ok` (il processo è vivo, senza controllare il database). `/readyz` risponde `ok` solo se Postgres è raggiungibile in tempo utile.
- `GET /health`, `/resources`, `/resources/{resourceId}`: le operazioni del contratto OpenAPI, generate in `internal/openapi` da `api/openapi.yaml` (vedi `scripts/generate-api.sh`, non modificare a mano). Servite senza prefisso `/v1`: è il gateway a esporre `/v1/*` e a rimuovere il prefisso instradando qui (vedi `services/gateway/internal/proxy`). `GET /health` fa lo stesso controllo di `/readyz` (verifica il database): il gateway la chiama per la propria readiness verso core.
- **Identità (GIT-54)**: core non autentica nessuno, si fida del gateway. Tutte le rotte tranne `/healthz`, `/readyz` e `/health` (pubblica nel contratto) richiedono gli header `X-Gitstack-User-Id/-Username/-Scopes/-Timestamp/-Signature` firmati dal gateway con il segreto di servizio (HMAC-SHA256, finestra di 60 s, schema in `internal/trust`); senza, o con una firma non valida, rispondono 401 `unauthenticated`. Chi raggiunge core direttamente non può spacciarsi per un utente. Test: `internal/trust`, `TestRouter_IdentitaSoloDalGateway`.
- Le operazioni sulla risorsa (`internal/httpserver/resources.go`) leggono/scrivono tramite `internal/store` sullo schema `core`. La creazione (`POST /resources`) pubblica anche l'evento di prova (vedi "Evento di prova" più sotto).

### Migrazioni (`internal/migrate`)

SQL versionate e incorporate nel binario (`internal/migrate/sql/*.sql`, `embed.FS`), applicate con [golang-migrate](https://github.com/golang-migrate/migrate). La tabella di stato (`core.schema_migrations`) vive anch'essa nello schema `core` (nessuna tabella di GitStack fuori dal proprio schema).

- **Idempotenti**: `core.schema_migrations` tiene l'ultima versione applicata; rilanciare le migrazioni senza modifiche allo schema non fa nulla (nessun errore, nessuna riapplicazione).
- **Applicate all'avvio**: `core` (senza argomenti, equivalente a `core serve`) applica le migrazioni prima di avviare il server HTTP.
- **Job dedicato**: lo stesso binario supporta `core migrate up` (applica ed esce, senza avviare il server: utile come init container o Job Helm, GIT-8) e `core migrate down [N]` (rollback delle ultime N migrazioni, default 1).
- **Rollback documentato**: ogni `NNNN_nome.up.sql` ha il proprio `NNNN_nome.down.sql`. `core migrate down` è pensato per sviluppo/CI, non per la produzione: un rollback che elimina tabelle/colonne perde dati; in produzione si preferisce una migrazione "up" correttiva. Il rollback della 0001 (`0001_init_core_schema.down.sql`) rimuove tabella e indice ma non lo schema `core` stesso (può essere stato creato da un bootstrap esterno, vedi sotto).

### Schema dedicato e utente DB a permessi limitati (D6)

`internal/migrate.EnsureSchema` controlla prima `pg_namespace` ed esegue `CREATE SCHEMA` solo se manca davvero: con un ruolo a permessi limitati che possiede già lo schema (vedi sotto) ma senza `CREATE` sul database, un `CREATE SCHEMA IF NOT EXISTS` fallirebbe comunque con "permission denied for database" (Postgres controlla il privilegio `CREATE` prima di valutare `IF NOT EXISTS`). La migrazione 0001 non crea più lo schema: ci pensa `EnsureSchema`, prima di aprire la connessione di migrazione (la tabella di stato di golang-migrate, `core.schema_migrations`, ha già bisogno dello schema prima della 0001).

In un'installazione reale, un operatore (o un job Helm di GIT-8) pre-crea lo schema e un ruolo Postgres dedicato con permessi solo su di esso, prima del primo avvio: vedi `migrations/bootstrap-role.sql` (da eseguire manualmente con un ruolo superuser/CREATEROLE, non da `core`, che non deve avere quel privilegio). Il ruolo è owner del solo schema `core`, e lo script revoca `CREATE` su `public` dal ruolo implicito `PUBLIC` (non basterebbe revocarlo dal singolo ruolo `core_app`: i privilegi su `public` arrivano da `PUBLIC`, non da un grant diretto). Verificato da un test d'integrazione che esegue davvero questo script (`internal/migrate/bootstrap_integration_test.go`): applica le migrazioni e fa CRUD con `core_app`, e verifica che `core_app` non possa creare tabelle in `public`.

### Evento di prova

Alla creazione della risorsa di prova, core pubblica un evento (`core.resource.test.created`, versione 1) con la libreria condivisa di GIT-6 (`pkg/events` del workspace, NATS JetStream, envelope nome/versione/id/timestamp/payload), non con codice NATS scritto a mano qui: `internal/events.NATSPublisher` (`nats.go`) adatta `pkg/events.Publisher` all'interfaccia `Publisher` che `CreateResource` usa per pubblicare. Nome, versione e payload dell'evento sono alias di `pkg/events/testevent` (fonte di verità unica: non ridefiniti in core). `NoopPublisher` resta disponibile per i test o un ambiente senza NATS, ma non è più quello agganciato in produzione (`main.go`).

All'avvio del server (non per `core migrate up|down`, che non tocca NATS), `main.go` si connette a NATS, apre il contesto JetStream e assicura lo stream del dominio `core` (`EnsureStream`) prima di accettare richieste. Verificato da `internal/events/nats_integration_test.go` (tag `integration`) contro un `nats-server` reale avviato in-process (stessa tecnica di `pkg/events`, non serve Docker): publish con conferma JetStream, un consumer durevole indipendente (solo `pkg/events`, non passa da core) che riceve e valida lo schema, e una versione di schema sconosciuta scartata dal consumer senza crash.

### Configurazione (variabili d'ambiente)

| Variabile | Obbligatoria | Default | Descrizione |
|---|---|---|---|
| `GITSTACK_CORE_ADDR` | no | `:8080` | Indirizzo di ascolto HTTP |
| `GITSTACK_CORE_DB_URL` | sì | — | Stringa di connessione Postgres (es. `postgres://core_app:***@postgres:5432/gitstack?sslmode=disable`) |
| `GITSTACK_CORE_DB_MAX_CONNS` | no | `10` | Numero massimo di connessioni nel pool |
| `GITSTACK_CORE_MIGRATIONS_TIMEOUT` | no | `30s` | Timeout per l'applicazione delle migrazioni (formato `time.Duration` di Go) |
| `GITSTACK_CORE_NATS_URL` | sì, per `serve` | — | Indirizzo del bus NATS JetStream (es. `nats://nats:4222`); non serve a `migrate up\|down` |
| `GITSTACK_IDENTITY_SERVICE_SECRET` | sì, per `serve` | — | Segreto di servizio (Secret `<release>-identity-service`, chiave `secret`): con questo core verifica la firma dell'identità inoltrata dal gateway; non serve a `migrate up\|down`. Mai nei log |
| `GITSTACK_CORE_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` o `error` |
| `GITSTACK_GIT_URL`, `GITSTACK_CORE_PUBLIC_URL`, `GITSTACK_CORE_SSH_HOST`, `GITSTACK_CORE_SSH_PORT` | vedi sotto | — | Repo (M-03/E): servizio git, base HTTPS, host e porta SSH; vedi "Variabili d'ambiente dei repo" |

### Sviluppo locale

```
go work sync
cd services/core
go generate ./...   # rigenera internal/openapi da api/openapi.yaml (solitamente via scripts/generate-api.sh)
go build ./...
go test ./...
```

### Test d'integrazione (Postgres e NATS reali)

Con il build tag `integration`:

- `internal/migrate`, `internal/store`, `internal/httpserver`: avviano un Postgres reale con [testcontainers-go](https://golang.testcontainers.org/) (`internal/dbtest`), applicano le migrazioni e verificano CRUD, idempotenza/rollback delle migrazioni e il ciclo di vita HTTP completo (incluso l'aggancio dell'evento di prova a un publisher fittizio). Richiedono un demone Docker raggiungibile (o un servizio Postgres di CI equivalente).
- `internal/migrate/bootstrap_integration_test.go`: come sopra, ma esegue anche `migrations/bootstrap-role.sql` con il superuser del container ed esercita `core_app` (il ruolo a permessi limitati), incluso il divieto di creare tabelle in `public`.
- `internal/events`: avvia un `nats-server` reale in-process (JetStream abilitato, nessun Docker necessario) e verifica `NATSPublisher` end-to-end (publish con conferma, consumer durevole indipendente, versione di schema sconosciuta gestita senza crash).

```
go test -tags=integration ./...
```

In CI (`.github/workflows/ci.yml`, job `go`) gira dopo `go test ./... -race`: `ubuntu-latest` ha già un demone Docker raggiungibile, che testcontainers-go usa per Postgres; NATS non serve Docker (server in-process). Verificato anche in locale in questa sessione con un demone Docker reale disponibile: tutti i pacchetti verdi (vedi riassunto della revisione).

### Immagine Docker

`Dockerfile` multi-stage (build Go + distroless statico). Il contesto di build è questa cartella, non la radice del monorepo:

```
docker build -f services/core/Dockerfile services/core
```

Il tag e il push nel registry interno sono compito del job `registry` (GIT-2), non di questo Dockerfile. Il comando di default del container (`ENTRYPOINT ["/usr/local/bin/core"]`, nessun argomento) applica le migrazioni e avvia il server; per un job dedicato che applichi solo le migrazioni, sovrascrivere il comando con `["core", "migrate", "up"]`.

Build verificata end-to-end (entrambi gli stage) con un demone Docker reale disponibile in questa sessione: `docker build -f services/core/Dockerfile services/core` produce l'immagine; avviarla senza configurazione (`docker run --rm <immagine>`) fallisce in modo pulito con un log JSON strutturato che segnala `GITSTACK_CORE_DB_URL` mancante, come atteso.

## M-03/A (GIT-63): schema dei repo e modello dati per le PR

Migrazione `0002_repositories` (up/down in `internal/migrate/sql`).

- **Un repo è una risorsa (D-A).** Una riga di `core.resources` con
  `type='repo'` più una riga di dettaglio in `core.repositories`
  (`resource_id` PK, FK verso `core.resources` `ON DELETE CASCADE`). Motivo:
  grant, `permissions/check` e `readable-resources` di M-02 lavorano già su
  `core.resources` e funzionano senza modifiche.
- **Unicità.** `resources_type_name_key UNIQUE(type, name)` diventa un indice
  unico parziale `WHERE type <> 'repo'`: il nome di una *risorsa* repo non è
  globale, l'unicità vera è `UNIQUE(owner_type, owner_id, name)` su
  `core.repositories`, **senza** filtro su `deleted_at`, perché dopo
  l'eliminazione il nome resta occupato finché il repo non è cancellato
  davvero (R2). Gli altri tipi di risorsa mantengono l'unicità di prima. La
  down riporta il vincolo originale (e, per poterlo ricreare, elimina prima le
  righe `type='repo'`).
- **Vincoli in tabella.** `owner_type IN ('user','organization')`; `owner_id`
  è l'id in identity, senza FK fra schemi (sono database/schema separati);
  `name ~ '^[a-z0-9_-][a-z0-9._-]{0,99}$' AND name NOT LIKE '%.git'` (R11);
  `visibility IN ('private','internal')` default `private` (P7);
  `default_branch` default `main` (R4); `protect_default_branch` default
  `true` (R9); `archived_at` (R10) e `deleted_at` (R2) NULL.
- **Predisposizione PR (D-F, T-07 v1.1).** `core.repo_counters(repo_id PK,
  next_number)`: contatore per repo, condiviso con le future issues;
  `core.pull_requests` con `UNIQUE(repo_id, number)`,
  `CHECK(source_branch <> target_branch)` e `state IN ('open','closed','merged')`.
  Nessuna API: solo tabelle e vincoli minimi.
- **Handler.** Le operazioni `repos` rispondono 501 (`not_implemented`) fino
  a GIT-67. Scope, owner/visibilità in identity e API interna del servizio
  git: vedi [docs/repos.md](../../docs/repos.md).
- **Test.** `go test -tags=integration -count=1 ./internal/migrate/...`
  (Postgres reale con testcontainers): su/giù, stesso nome con owner diversi,
  stesso nome con lo stesso owner anche con `deleted_at`, `Repo` e `x.git`,
  unicità degli altri tipi.

## M-03/E (GIT-67): API dei repo

Operazioni del tag `repos`: `POST /repos`, `GET /repos`, `GET|PATCH /repos/{owner}/{repo}` (handler in `internal/httpserver/repos.go`, SQL in `internal/store/repos.go`). Eliminazione recuperabile (R2, GIT-68): `DELETE /repos/{owner}/{repo}`, `GET /repos/deleted`, `POST /repos/deleted/{repoId}/restore` (`internal/httpserver/repos_trash.go`). Il job `internal/repopurge` (ogni ora, FOR UPDATE SKIP LOCKED, sicuro con più repliche) cancella dopo 7 giorni riga, disco (API interna di git) e grant (`DELETE /internal/resources/{id}` di identity).

- **Permessi.** Core non decide da solo: chiede a identity (`internal/identityclient`: `ResolveOwner`, `SetResourceAttributes`, `HasRole` = `/internal/permissions/check`, `ReadableResources`). Lettura/elenco: `read`; impostazioni: `admin` (403 se legge soltanto). Un repo che il chiamante non può leggere risponde **404**, identico a uno inesistente.
- **Creazione** (`POST /repos`). Ordine: validazione (nome con `pkg/names`, R11; visibilità `private` se il campo manca, P7) → owner da identity (404) → attributi del nuovo repo in identity e verifica che il creatore sia `admin` del repo che sta nascendo, cioè sé stesso (P6), owner dell'organizzazione (P1) o amministratore di sistema (403 altrimenti) → una transazione con `core.resources`, `core.repositories` e `core.repo_counters(repo_id, next_number = 1)` (I1; 409 se il nome è occupato per quell'owner, anche da un repo eliminato) → il servizio git crea il repo su disco (R5) → grant admin al creatore → commit. Se git o il grant falliscono la transazione si annulla (nessuna riga in core) e il repo già nato su disco viene tolto (cestino + cancellazione). *Limite noto:* se la verifica del permesso (o un 409) rifiuta la richiesta, in identity resta una riga `resource_attributes` per un id mai usato: innocua (nessuna risorsa in core la richiama).
- **Impostazioni** (`PATCH`): `description`, `visibility` (aggiorna anche identity, annullando tutto se identity non risponde), `defaultBranch` (R4: deve essere fra i branch che `GET /internal/git/repos/{id}` elenca in `branches`), `protectDefaultBranch` (R9, `true` di default), `archived` (R10). Un repo archiviato risponde **409** `archived` a qualunque modifica tranne `{"archived": false}` da solo. Le modifiche si serializzano con `SELECT … FOR UPDATE`.
- **Owner in core.** La migrazione `0003_repositories_owner_name` aggiunge `core.repositories.owner_name`, copia del nome scritta alla creazione: serve a risolvere `/repos/{owner}/{repo}` e a comporre `fullName` e indirizzi di clone senza una chiamata a identity per riga (rinomina dell'owner: fuori dalla v1, R3).
- **`empty`** viene da git (`GET /internal/git/repos/{id}`); se git non risponde l'elenco e la lettura ripiegano su `false` e lo loggano, le modifiche no (503).
- **Indirizzi di clone** (R1, R7): `https` = `<PUBLIC_URL>/<owner>/<repo>.git` (senza PUBLIC_URL, la base della richiesta); `ssh` = sempre `ssh://git@<host>:<porta>/<owner>/<repo>.git` (porta 2222 di default); `sshShort` = `git@<host>:<owner>/<repo>.git`, presente **solo con porta 22** (campo opzionale aggiunto a `RepoCloneUrls`).
- **API interna di git** (`internal/gitclient`): chiamate con gli header `X-Gitstack-*` firmati col segreto di servizio, con l'identità di chi ha fatto la richiesta a core. `GET /internal/git/repos/{id}` ha ora anche `branches` (aggiunto a `GitRepoState`, `services/git`).

### Variabili d'ambiente dei repo (le usa anche il chart, GIT-74)

| Variabile | Obbligatoria | Default | Descrizione |
|---|---|---|---|
| `GITSTACK_GIT_URL` | no | — | URL interno del servizio git (es. `http://git:8080`). Senza, un warn all'avvio e creare/modificare i repo risponde 503 (come senza `GITSTACK_IDENTITY_URL`); il chart la emette solo con `git.enabled` |
| `GITSTACK_CORE_PUBLIC_URL` | consigliata | — | Base HTTPS pubblica per gli indirizzi di clone (es. `https://git.example.com`). Senza, un warn all'avvio e gli indirizzi si compongono dalla richiesta: `X-Forwarded-Proto`/`X-Forwarded-Host` (scritti dal gateway), altrimenti `Host` con `http`; l'host SSH di default diventa quello |
| `GITSTACK_CORE_SSH_HOST` | no | host di `PUBLIC_URL` (o della richiesta) | Host dell'indirizzo SSH |
| `GITSTACK_CORE_SSH_PORT` | no | `2222` | Porta SSH dell'installazione (R7); `off` = SSH spento: `cloneUrls` senza `ssh` né `sshShort` |

Il segreto di servizio è `GITSTACK_IDENTITY_SERVICE_SECRET` e `GITSTACK_IDENTITY_URL` serve a tutte le operazioni sui repo (senza, rispondono 503).

### Test

`internal/httpserver/repos_integration_test.go` (Postgres reale con testcontainers; identity e git sono fake in memoria che riproducono P1/P3/P6 e l'API interna): creazione (default private, riga `repo_counters` a 1, 403, nome non valido, 409, owner inesistente, git/grant che falliscono senza righe a metà), lettura (404 per il repo privato di altri), elenco filtrato e paginato, impostazioni (branch, protezione, visibilità, archiviazione e riattivazione). Unit: `repos_clone_test.go` (R7), `internal/gitclient`, `internal/identityclient`, `internal/config`.

## Letture del codice (M-04)

Le letture di un repo (albero, file, raw, branch, tag, storico, commit con diff, blame, ZIP, lingue, README) passano da core: risolve owner/nome → repoId, applica `read` e `read:resource`, poi chiama l'API interna del servizio git per repoId, con raw e ZIP in streaming; il gateway non parla mai col servizio git. Gli handler (`internal/httpserver/repos_code.go`) rispondono 501 fino a GIT-84. Motivo, errori, autore→utente e limiti: [docs/repos.md](../../docs/repos.md#letture-del-codice-m-04-git-80).
