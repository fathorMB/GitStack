# core

API di repo (metadati), issues, commenti, etichette, milestone, notifiche, webhook; consuma eventi dal bus NATS (es. `fixes #12` chiude la issue). API complete arrivano con le milestone successive a M-01/T-05.

## M-01/T-05 (questo task)

In M-01, core implementa solo la risorsa di prova del contratto (`Resource`, schema generico D15 [c_4ef209e79c5b2ddb]), su uno schema Postgres dedicato ("core", D6 [c_4df04d65b3ac4910]): CRUD completo, migrazioni versionate, health/readiness che verificano il database. Nessuna tabella condivisa con altri servizi: identity, git e core (quando arriveranno le loro migrazioni) hanno ciascuno il proprio schema.

- `GET /healthz`, `GET /readyz`: probe di liveness/readiness (convenzione k8s), non fanno parte del contratto pubblico `api/openapi.yaml`. `/healthz` risponde sempre `ok` (il processo è vivo, senza controllare il database). `/readyz` risponde `ok` solo se Postgres è raggiungibile in tempo utile.
- `GET /health`, `/resources`, `/resources/{resourceId}`: le operazioni del contratto OpenAPI, generate in `internal/openapi` da `api/openapi.yaml` (vedi `scripts/generate-api.sh`, non modificare a mano). Servite senza prefisso `/v1`: è il gateway a esporre `/v1/*` e a rimuovere il prefisso instradando qui (vedi `services/gateway/internal/proxy`). `GET /health` fa lo stesso controllo di `/readyz` (verifica il database): il gateway la chiama per la propria readiness verso core.
- Le operazioni sulla risorsa (`internal/httpserver/resources.go`) leggono/scrivono tramite `internal/store` sullo schema `core`. La creazione (`POST /resources`) pubblica anche l'evento di prova (vedi "Evento di prova" più sotto).

### Migrazioni (`internal/migrate`)

SQL versionate e incorporate nel binario (`internal/migrate/sql/*.sql`, `embed.FS`), applicate con [golang-migrate](https://github.com/golang-migrate/migrate). La tabella di stato (`core.schema_migrations`) vive anch'essa nello schema `core` (nessuna tabella di GitStack fuori dal proprio schema).

- **Idempotenti**: `core.schema_migrations` tiene l'ultima versione applicata; rilanciare le migrazioni senza modifiche allo schema non fa nulla (nessun errore, nessuna riapplicazione).
- **Applicate all'avvio**: `core` (senza argomenti, equivalente a `core serve`) applica le migrazioni prima di avviare il server HTTP.
- **Job dedicato**: lo stesso binario supporta `core migrate up` (applica ed esce, senza avviare il server: utile come init container o Job Helm, GIT-8) e `core migrate down [N]` (rollback delle ultime N migrazioni, default 1).
- **Rollback documentato**: ogni `NNNN_nome.up.sql` ha il proprio `NNNN_nome.down.sql`. `core migrate down` è pensato per sviluppo/CI, non per la produzione: un rollback che elimina tabelle/colonne perde dati; in produzione si preferisce una migrazione "up" correttiva. Il rollback della 0001 (`0001_init_core_schema.down.sql`) rimuove tabella e indice ma non lo schema `core` stesso (può essere stato creato da un bootstrap esterno, vedi sotto).

### Schema dedicato e utente DB a permessi limitati (D6)

Lo schema `core` è creato (`CREATE SCHEMA IF NOT EXISTS core`) dalla migrazione 0001 se non esiste già: comodo per sviluppo/CI a database vuoto. In un'installazione reale, un operatore (o un job Helm di GIT-8) può invece pre-creare lo schema e un ruolo Postgres dedicato con permessi solo su di esso, prima del primo avvio: vedi `migrations/bootstrap-role.sql` (da eseguire manualmente con un ruolo superuser/CREATEROLE, non da `core`, che non deve avere quel privilegio). Con lo schema già presente, la migrazione 0001 diventa un no-op sulla parte `CREATE SCHEMA`.

### Evento di prova

Alla creazione della risorsa di prova, core pubblica un evento (`core.resource.test.created`, versione 1: vedi `internal/events`). Nota vincolante del CTO (GIT-5/GIT-6): l'evento si pubblica con la libreria condivisa di GIT-6 (NATS JetStream, envelope nome/versione/id/timestamp/payload), non con codice NATS scritto a mano qui.

Stato a questo commit: GIT-6 è ancora in corso (libreria non ancora disponibile in questo modulo). `internal/events` espone già l'interfaccia `Publisher` che `CreateResource` usa per pubblicare; in produzione (`main.go`) è agganciato un `NoopPublisher` che logga soltanto. Il test d'integrazione di `internal/httpserver` (`router_integration_test.go`, tag `integration`) verifica comunque che `CreateResource` chiami `Publisher.Publish` con il nome, la versione e il payload attesi, tramite un publisher fittizio: la verifica che l'evento arrivi davvero su NATS JetStream reale è nel perimetro di GIT-6. Quando GIT-6 è approvato, agganciare la libreria è un cambiamento locale a `internal/events` (un adapter che implementa `Publisher`); nessun'altra parte di core cambia.

### Configurazione (variabili d'ambiente)

| Variabile | Obbligatoria | Default | Descrizione |
|---|---|---|---|
| `GITSTACK_CORE_ADDR` | no | `:8080` | Indirizzo di ascolto HTTP |
| `GITSTACK_CORE_DB_URL` | sì | — | Stringa di connessione Postgres (es. `postgres://core_app:***@postgres:5432/gitstack?sslmode=disable`) |
| `GITSTACK_CORE_DB_MAX_CONNS` | no | `10` | Numero massimo di connessioni nel pool |
| `GITSTACK_CORE_MIGRATIONS_TIMEOUT` | no | `30s` | Timeout per l'applicazione delle migrazioni (formato `time.Duration` di Go) |
| `GITSTACK_CORE_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` o `error` |

### Sviluppo locale

```
go work sync
cd services/core
go generate ./...   # rigenera internal/openapi da api/openapi.yaml (solitamente via scripts/generate-api.sh)
go build ./...
go test ./...
```

### Test d'integrazione (Postgres reale)

I test in `internal/migrate`, `internal/store` e `internal/httpserver` con il build tag `integration` avviano un Postgres reale con [testcontainers-go](https://golang.testcontainers.org/) (`internal/dbtest`), applicano le migrazioni e verificano CRUD, idempotenza/rollback delle migrazioni e il ciclo di vita HTTP completo (incluso l'aggancio dell'evento di prova):

```
go test -tags=integration ./...
```

Richiedono un demone Docker raggiungibile (o un servizio Postgres di CI equivalente). In questo ambiente di sviluppo il demone Docker non è raggiungibile (`docker info` fallisce): la compilazione con il tag `integration` è verificata (`go build -tags=integration ./...`, `go vet -tags=integration ./...`), l'esecuzione no; vanno verificati in CI o in un ambiente con Docker attivo.

### Immagine Docker

`Dockerfile` multi-stage (build Go + distroless statico). Il contesto di build è questa cartella, non la radice del monorepo:

```
docker build -f services/core/Dockerfile services/core
```

Il tag e il push nel registry interno sono compito del job `registry` (GIT-2), non di questo Dockerfile. Il comando di default del container (`ENTRYPOINT ["/usr/local/bin/core"]`, nessun argomento) applica le migrazioni e avvia il server; per un job dedicato che applichi solo le migrazioni, sovrascrivere il comando con `["core", "migrate", "up"]`.
