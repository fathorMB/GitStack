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

In CI (`.github/workflows/ci.yml`, job `go`) gira dopo `go test ./... -race`: `ubuntu-24.04` ha già un demone Docker raggiungibile, che testcontainers-go usa per Postgres; NATS non serve Docker (server in-process). Verificato anche in locale in questa sessione con un demone Docker reale disponibile: tutti i pacchetti verdi (vedi riassunto della revisione).

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

## Issues (M-05/A, GIT-101): contratto e schema

Il contratto è in `api/openapi.yaml` (tag `issues`, regole I1–I11 in `.prisma/knowledge/topics/issues.md`); lo schema è la migrazione `0004_issues`. In questo item gli handler (`internal/httpserver/issues.go`) rispondono **501** `not_implemented`; il gateway li instrada a core come le letture di M-04 (dichiarazioni di sicurezza generate dal contratto). Fa eccezione la creazione del repo, che riceve `defaultLabels` e crea le etichette predefinite (I5).

**Operazioni** (sotto `/repos/{owner}/{repo}`): `issues` (elenco/ricerca, creazione), `issues/{number}` (lettura, modifica di titolo e testo), `close` (motivo `completed|not_planned|duplicate` con `duplicateOf`) e `reopen`, `hidden` (admin), `lock` (PUT/DELETE, admin), `assignees`, `labels`, `milestone` (PUT che sostituiscono), `events`, `versions` (admin), `comments` e `comments/{commentId}` (+ `versions`), `issue-attachments` (upload multipart e download), `issue-templates`, `labels` e `milestones` come risorse del repo; in più `GET /search/issues` per la ricerca su tutta l'installazione (I10).

**Permessi (I3).** `read` apre issues e commenta; `write` assegna, mette etichette e milestone, chiude e riapre anche le issues altrui; l'autore chiude e riapre la propria e ne modifica il testo; `admin` blocca (I11), nasconde, vede le versioni precedenti (I4) ed elimina i commenti altrui. Un repo non leggibile, eliminato o inesistente dà **404** (mai 403); un repo archiviato rifiuta ogni modifica con **409** `archived` (R10). Una issue nascosta è **404** per chi non ha `admin` (scelta: un 404 e non un 200 svuotato, per non rivelare cosa è stato nascosto; il numero resta occupato).

**Errori.** 400 richiesta malformata, 401 senza credenziali, 403 permesso mancante (anche `locked`), 404, 409 (`archived`, `already_closed`, `already_open`, `already_exists`), 413 (`body_too_large` oltre 1 MiB, `attachment_too_large`), 422 `validation_failed` con `details.fields` (titolo vuoto, assegnatari oltre 10 o senza `write`, etichetta o milestone inesistente, `duplicateOf` non valido, tipo di allegato non ammesso).

### Numerazione (I1)

Un solo contatore per repo, `core.repo_counters.next_number`, condiviso da issues e PR (`core.pull_requests`). Il numero si prende **nella stessa transazione dell'INSERT**:

```sql
UPDATE core.repo_counters SET next_number = next_number + 1
 WHERE repo_id = $1 RETURNING next_number - 1;
INSERT INTO core.issues (id, repo_id, number, ...) VALUES (..., $n, ...);
```

L'UPDATE blocca la riga del contatore fino al commit: due transazioni concorrenti ricevono numeri diversi, e se una annulla il contatore torna indietro con lei (nessun buco). Un numero committato non si riusa mai: le issues non si eliminano (I4), nascondere conserva il numero. Garanzie in DB: `UNIQUE (repo_id, number)` su `issues` e su `pull_requests`, più un trigger `BEFORE INSERT` su entrambe (`core.check_number_not_taken`) che rifiuta con `23505` un numero già usato **dall'altra** tabella, cioè un INSERT che non è passato dal contatore (lo `UNIQUE` non vede l'altra tabella: è il controllo documentato contro la collisione). La riga del contatore la crea sempre la creazione del repo; la migrazione la crea per i repo preesistenti (`ON CONFLICT DO NOTHING`) e porta `next_number` oltre l'eventuale massimo delle PR. Le milestone hanno un numero per repo indipendente, da `next_milestone_number` (stesso schema `UPDATE … RETURNING`).

Test: `internal/migrate/issues_integration_test.go` (60 transazioni in parallelo fra issue e PR dallo stesso contatore: numeri consecutivi e senza duplicati; collisione fra le tabelle; rollback; salita e discesa pulite di `0004` su Postgres reale).

### Altre scelte dello schema

- **Chiusura (I2):** `close_reason` e `closed_at` ci sono se e solo se `state = 'closed'` (CHECK); `duplicate_of` (con FK composita `(repo_id, duplicate_of)` → `issues(repo_id, number)`: stesso repo) c'è se e solo se il motivo è `duplicate`. Riaprire azzera i tre campi. `closedIssues` di una milestone conta solo `completed`.
- **Testi e cronologia (I4):** la versione corrente sta in `issues`/`issue_comments`; ogni modifica copia quella sostituita in `issue_text_versions` (numerate per issue o per commento). Un commento eliminato resta con `deleted_at` e testo vuoto; gli eventi stanno in `issue_events` (`type` in un elenco chiuso, dettagli in `data` JSONB, `actor_id` NULL per gli eventi di sistema come `fixes #n`).
- **Assegnatari (I6):** `issue_assignees`, massimo 10 imposto da un trigger che blocca la riga della issue (`23514`); che abbiano `write` lo verifica l'handler con identity.
- **Etichette (I5):** per repo, nome unico senza distinguere maiuscole (`lower(name)`), senza `/`, virgole e caratteri di controllo; `core.seed_default_labels(repo_id)` crea le otto predefinite (`bug`, `enhancement`, `documentation`, `question`, `duplicate`, `good first issue`, `agent-ready`, `needs-human`), idempotente, chiamata dalla creazione del repo se `defaultLabels` non è `false`. I repo già esistenti non le ricevono.
- **Ricerca (I10):** colonna `search tsvector` generata (`to_tsvector('simple', title || ' ' || body)`) con indice GIN, su `issues` e su `issue_comments`; configurazione `simple` perché i testi sono misti italiano/inglese. Il traduttore di `q` (`is:`, `label:`, …) spetta all'item di ricerca.
- **Utenti:** gli `*_id` utente sono UUID senza FK (identity è un altro schema), come `owner_id`.

### Allegati (I9, decisione del CTO)

- I file vivono su un PVC dedicato `attachments-data`, montato da core in `/var/lib/gitstack/attachments` (configurabile con `GITSTACK_CORE_ATTACHMENTS_DIR`), con percorso `<repo_id>/<attachment_id>` **senza il nome originale**, che sta solo nei metadati in DB (`core.issue_attachments`: `filename`, `content_type`, `size_bytes`; nessun file nel database).
- Limite `GITSTACK_CORE_ATTACHMENTS_MAX_BYTES`, default `10485760` (10 MB, I9): oltre, 413 `attachment_too_large`. Tipi ammessi: immagini, PDF, testo/log, ZIP (il tipo si verifica sui byte, 422 se non ammesso). Si scaricano solo con `getIssueAttachment`, autenticati e con `read` sul repo, sempre come allegato (`nosniff`, `Content-Security-Policy: sandbox`): nessun URL pubblico.
- L'upload crea un allegato non collegato (`issue_id` NULL), che si collega con `attachmentIds` alla creazione di una issue o di un commento; i non collegati da più di 24 ore (`GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL`, default `24h`) li elimina il job `internal/attachments.Cleaner` (file e riga, ogni 15 minuti, `FOR UPDATE SKIP LOCKED`: sicuro con più repliche; orologio iniettabile, provato in `internal/attachments/cleanup_integration_test.go`). Un allegato non ancora collegato lo scarica solo chi l'ha caricato. `store.LinkAttachments(ctx, tx, repoID, uploaderID, issueID, commentID, ids)` collega gli allegati non collegati dello stesso repo e dello stesso utente dentro la transazione del chiamante (tutto o niente, `ErrAttachmentNotLinkable` altrimenti): la usa GIT-104 per `attachmentIds` di issue e commenti.
- Il volume fa parte del set di backup di D19 insieme a Postgres e `git-data` (lo realizza M-08; qui è solo scritto).
- Con il volume RWO, core con gli allegati attivi richiede `replicaCount: 1` e `strategy: Recreate`: lo applica il chart in GIT-107, non questo item. 
**Implementazione (GIT-107).** Le variabili sono lette da `internal/config`: `GITSTACK_CORE_ATTACHMENTS_DIR` (vuota = upload e download rispondono 503 `attachments_unavailable`), `GITSTACK_CORE_ATTACHMENTS_MAX_BYTES` (default 10 MiB) e `GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL` (default 24h). L'upload (`httpserver/attachments.go`) legge il multipart in streaming (nessun buffer in memoria), scrive su un file temporaneo e lo rinomina in `<repo_id>/<id>`; chi vede il repo (`read`) può caricare, un repo archiviato risponde 409. Il tipo si ricava dai byte (`attachments.Detect`: firme PNG, JPEG, GIF, WebP, PDF, ZIP e testo/log), mai dall'estensione né dal `Content-Type` del client; **HTML, XML e SVG sono rifiutati** (422 `unsupported_media_type`), anche se sembrano testo: un testo che comincia con `<` non è quindi ammesso come allegato. Il download risponde sempre con il tipo salvato (mai html/xml/svg), `Content-Disposition: attachment` (nome con codifica RFC 6266/2231, mai nel percorso su disco), `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox` e `Cache-Control: private, no-store`, come il raw di B3; 401 senza credenziali, 404 senza `read` o per un allegato di un altro repo, e per un allegato di una issue nascosta a chi non ha `admin`. La cancellazione definitiva di un repo (`repopurge`) toglie anche la sua cartella degli allegati. Un file scritto senza che la riga arrivi nel database (crollo fra i due passi) resta sul volume: non c'è una scansione dei file senza riga.

### Decisioni di contratto da conoscere

- Etichette per **nome** nel percorso (`/labels/{name}`, spazi codificati) e nelle operazioni sulle issue; milestone per **numero**.
- Gli elenchi hanno `{items, page, perPage, total}` come `GET /repos`; `listIssues` restituisce `IssueSummary` (senza testo).
- `PUT .../assignees|labels|milestone` sostituiscono l'insieme: un agente che «prende» il lavoro si autoassegna con `PUT` mantenendo gli altri assegnatari (I6).
- Modelli (I11): `listIssueTemplates` leggerà `.gitstack/ISSUE_TEMPLATE/` dal branch principale tramite il servizio git (API interne `gitGetTree` e `gitGetFile` già esistenti).


## Ricerca delle issues (M-05/F, GIT-106)

`GET /repos/{owner}/{repo}/issues` e `GET /search/issues` usano la stessa traduzione (`internal/httpserver/issues_search.go`) della sintassi di `pkg/issuequery` (I10) in SQL.

- **Mai testo dell'utente nell'SQL.** Ogni valore (qualificatori, testo libero, filtri espliciti) è un parametro `$n`; le parti di SQL sono costanti. Il testo libero usa `plainto_tsquery` / `phraseto_tsquery` (configurazione `simple`, come l'indice): operatori come `&`, `|`, `!` sono testo. NUL e UTF-8 non valido sono 422.
- **Cosa si cerca.** Titolo e testo (`issues.search`) e commenti non eliminati (`issue_comments.search`), con gli indici GIN della migrazione 0004. `relevance` ordina per `ts_rank` (massimo fra issue e commenti).
- **Semantica dei qualificatori ripetuti.** `is:`, `label:`, `assignee:`, `no:` e il testo devono valere tutti (AND); `reason:`, `author:`, `milestone:`, `repo:` e `org:` sono alternative (OR); ogni `-` esclude. `milestone:` accetta titolo o numero. `assignee:@me` è il chiamante; `@agents` sono gli utenti di tipo `agent` (identity `lookup-ids`) fra gli autori/assegnatari dell'ambito. Un login sconosciuto non trova nulla. Su `listIssues` i filtri espliciti si sommano a `q`; `state` vale `open` solo se `q` non ha `is:open|closed`.
- **Visibilità.** La ricerca globale parte dai repo di readable-resources di identity (`all` per l'admin di sistema, visibilità interna compresa) e non esamina mai gli altri: `i.repo_id = ANY(leggibili)`; i repo eliminati sono esclusi. Le issues nascoste compaiono solo dove il chiamante è admin: core verifica `admin` soltanto sui repo leggibili che hanno issues nascoste (al massimo 200), non su tutti.
- **Come si limita il costo.**
  1. *Indici*: ricerca testuale con GIN (`issues_search_idx`, `issue_comments_search_idx`); filtri per repo/stato con `issues_repo_state_idx`, etichette e assegnatari con gli indici di `issue_labels` e `issue_assignees`.
  2. *LIMIT*: la pagina ha al massimo 100 righe; il totale si conta con `LIMIT 10000` (oltre, `total` vale 10000) e le pagine oltre i primi 10000 risultati sono 422; `@agents` classifica al massimo 5000 utenti.
  3. *Tempo massimo*: ogni ricerca gira in una transazione con `statement_timeout` di 5 s; oltre risponde 503 `search_timeout` («restringi la ricerca»).
  Un `repo:` o `org:` restringe l'ambito più di ogni altro filtro. Non c'è un indice sugli hidden: la query sui repo con nascoste è una scansione per repo; se pesasse con milioni di issues si aggiunge un indice parziale `(repo_id) WHERE hidden`.

## Notifiche, iscrizioni e webhook (M-06/A, GIT-129): contratto e schema

Contratto in `api/openapi.yaml` (tag `notifications` e `webhooks`, regole C1–C9 in `.prisma/knowledge/topics/collegamenti-notifiche-webhook.md`); schema nella migrazione `0006_notifications_webhooks`; payload e intestazioni dei webhook in `docs/webhooks.md`; eventi di dominio in `docs/events.md`. In questo item gli handler (`internal/httpserver/notifications.go`) rispondono **501** `not_implemented`; il gateway li instrada a core, anche `/orgs/{org}/hooks…` (che il gateway altrimenti manderebbe a identity con `/orgs/{rest...}`: il pattern più specifico vince).

**Operazioni.** Casella dell'utente corrente: `GET/DELETE /notifications` (filtri `reason`, `state`, `repo`), `POST /notifications/read-all`, `GET/PATCH/DELETE /notifications/{notificationId}`; `PUT/GET/DELETE /repos/{owner}/{repo}/issues/{number}/subscription` (Subscribe/Unsubscribe); `GET/PUT/DELETE /repos/{owner}/{repo}/watch` (`participating` default, `all`, `ignore`); `GET/PUT /user/notification-preferences`. Webhook, identici sotto `/repos/{owner}/{repo}` e `/orgs/{org}`: `hooks` (elenco, creazione), `hooks/{hookId}` (lettura, modifica, eliminazione), `reactivate`, `deliveries`, `deliveries/{deliveryId}` e `…/redeliver`. La cronologia della issue ha tre tipi nuovi: `referenced_from`, `commit_linked`, `closed_by_commit`.

**Permessi.** Notifiche, iscrizioni e preferenze sono sempre dell'utente corrente (persona o agente, C4): nessun percorso con lo username. Subscribe e Watch chiedono `read` sul repo (404 se non si legge). Webhook di repo: `admin` sul repo (403 con `read`/`write`, 404 se non si legge); webhook di organizzazione: ruolo `owner` (403 a un membro, 404 a chi non vede l'organizzazione). Scope dei token: `read:resource`/`write:resource` per le notifiche e i webhook di repo, `read:org`/`write:org` per quelli di organizzazione. Il contratto non ha `x-required-permission` su queste rotte: il permesso lo controlla l'handler con identity (come per le issues).

### Segreto dei webhook (decisione del CTO)

Il segreto è **cifrato, non hash**: `X-GitStack-Signature` è un HMAC-SHA256 calcolato a ogni consegna e serve il segreto in chiaro. Con un hash non si potrebbe firmare.

- Cifratura **AES-256-GCM** in core, nelle colonne `core.webhooks.secret_ciphertext` (BYTEA, testo cifrato col tag GCM), `secret_nonce` (BYTEA, 12 byte, casuale a ogni scrittura) e `secret_key_id` (TEXT, quale chiave ha cifrato: serve a ruotarla). Le tre colonne sono tutte valorizzate o tutte `NULL` (CHECK).
- La chiave è di 32 byte e arriva dalla variabile d'ambiente `GITSTACK_WEBHOOK_SECRET_KEY`, in base64, presa da un Secret del chart generato all'installazione con `helm.sh/resource-policy: keep` (come gli altri segreti generati). Chart e lettura della chiave sono di M-06/G: in questo item ci sono solo lo schema e la documentazione. Senza chiave, core non crea webhook con segreto.
- Nel contratto `secret` è `writeOnly`: **non torna mai** nelle risposte, che hanno solo `hasSecret: boolean`. Un webhook senza segreto è valido (consegna non firmata, come GitHub).
- Rotazione futura: una chiave nuova ha un `secret_key_id` nuovo; core legge con la chiave indicata nella riga e riscrive con la corrente.

### Altre scelte dello schema

- **Casella (C3, C4, C9):** `core.notifications` ha `read_at` e `archived_at` (non lette = `read_at IS NULL`), `reason` (stesso elenco delle preferenze email), `event_name` e `data` per il testo, `email_due_at`/`email_sent_at` per il raggruppamento di pochi secondi per issue (C5). Le lette si eliminano dopo 90 giorni (indice su `read_at`), le non lette restano; le notifiche di un repo che l'utente non legge più le elimina l'applicazione. Le FK a repo, issue, commento e webhook sono `ON DELETE CASCADE`.
- **Iscrizioni (C3):** `core.issue_subscriptions` (`subscribed=false` è un Unsubscribe esplicito e non viene più riacceso dall'iscrizione automatica), `core.repo_watches` (nessuna riga = `participating`), `core.notification_preferences` (una riga per utente e tipo; nessuna riga = default: email per `mentioned` e `assigned`).
- **Webhook (C6, C7):** `core.webhooks` (`scope` `repo` o `org`; `org_id` è l'id in identity, senza FK; `events` un array non vuoto di `push`, `issues`, `issue_comment`, `repository`; `failing_since`, `disabled_at` e `disabled_reason` per la disattivazione dopo 3 giorni di fallimenti). `core.webhook_deliveries`: una riga per consegna, con l'ultimo tentativo, `payload`, intestazioni e risposta (`response_body` al massimo 4096 byte, C8); `redelivery_of` lega una *Redeliver* all'originale; un indice univoco su `(webhook_id, source_event_id)` evita due consegne dello stesso evento NATS. Le consegne si conservano 30 giorni (il job di pulizia usa `created_at`).
- **Collegamenti (C1, C2):** `core.issue_commit_links` (una riga per issue, repo del commit e sha; `close_keyword` e `closed_applied_at`: chi chiude lo fa una volta sola, e una issue riaperta non viene richiusa dallo stesso commit) e `core.issue_references` (`#n` e `owner/repo#n` da issue, PR o commenti; la visibilità «vede entrambi i repo» si controlla in lettura). `core.issue_references` usa `ON CONFLICT DO NOTHING` per idempotenza; **togliere un riferimento dal testo non cancella le righe**, come su GitHub (documentato in `issue_references.go`). `issue_events.type` ammette i tre tipi nuovi; `referenced` resta nel vincolo ma non si scrive più.
- Rollback: il down elimina tutte queste tabelle (segreti compresi) e le righe di cronologia dei tre tipi nuovi.

Test: `internal/migrate/notifications_integration_test.go` (vincoli, unicità, cascata e salita/discesa/salita pulite di `0006` su Postgres reale).
