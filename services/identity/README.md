# identity

Utenti locali, sessioni, token personali con scope, chiavi SSH, login OIDC esterno, organizzazioni, team e permessi (M-02, D5). Questo item (GIT-29) contiene solo il **contratto** (`api/openapi.yaml`), lo **schema del database** (`internal/migrate/sql`) e questa nota di architettura: nessun handler, nessuna logica.

## Contratto API

Le operazioni di identity sono in `api/openapi.yaml` con i tag `auth`, `users`, `tokens`, `ssh-keys`, `organizations`, `teams`, `permissions` (pubbliche, sotto `/v1` dal gateway) e `internal` (solo fra servizi). Il gateway e core escludono questi tag dalla propria interfaccia generata (`exclude-tags` in `internal/openapi/oapi-codegen.yaml`); il servizio identity genererà la sua con i tag complementari.

Catalogo degli scope dei token (schema `TokenScope`, deciso dal CTO, non allargare senza passare da lui): `read:user`, `write:user`, `read:org`, `write:org`, `admin:org`, `read:resource`, `write:resource`. Ogni operazione dichiara gli scope richiesti in `x-required-scopes`. Le sessioni web non hanno scope: valgono i permessi dell'utente. Il CHECK `api_tokens_scopes_known` ripete lo stesso catalogo nel database: aggiungere uno scope richiede contratto **e** una migrazione.

Errori: formato unico `Error` con codici stabili per 401/403/404/409/422 (vedi la descrizione dello schema). `insufficient_scope` (403) porta `details.required`; `validation_failed` (422) porta `details.fields`.

## Confine gateway / identity (D4)

| | gateway | identity |
|---|---|---|
| Espone | unico punto d'ingresso pubblico `/v1/*` | nulla all'esterno: solo rete del cluster |
| Sa | instradare, verificare che la credenziale sia valida, applicare gli scope, rate limit | chi è l'utente, quali credenziali ha, appartenenze e grant |
| Non sa | password, hash, chiavi, membership | routing, rate limit, cache HTTP |
| Stato | nessun database; solo una cache in memoria | schema Postgres `identity` (D6), nessun'altra tabella condivisa |

- **Autenticazione (chi sei)**: la fa identity. Il gateway non interpreta mai il contenuto di una credenziale: la passa a identity e si fida del `Principal` restituito.
- **Autorizzazione grossolana (scope)**: la applica il gateway usando `x-required-scopes` dell'operazione e gli `scopes` del principal; se mancano, risponde 403 `insufficient_scope` senza chiamare il servizio a valle. Le sessioni non hanno scope e passano.
- **Autorizzazione fine (ruolo su una risorsa, ruolo in org)**: la applica il servizio proprietario del dato (core, git, identity stessa), che chiede a identity `POST /internal/permissions/check` (o legge il principal per i casi banali). Il gateway non conosce le risorse.
- **Rotte di identity** (`/v1/auth/*`, `/v1/users*`, `/v1/user/*`, `/v1/orgs*`, `/v1/resources/{id}/grants*`, `/v1/resources/{id}/permissions`) sono instradate dal gateway a identity; il resto a core. `/v1/internal/*` **non** è mai instradato: il gateway lo rifiuta (404).
- **Interfaccia interna** (`/internal/verify`, `/internal/permissions/check`, `/internal/ssh-keys/{fingerprint}`): protetta dallo schema `serviceAuth` (segreto condiviso di servizio da un Secret k8s, mai un token utente) e raggiungibile solo dal cluster. `/internal/ssh-keys/{fingerprint}` serve al servizio git per mappare una chiave SSH a un utente.

## Come il gateway verifica sessioni e token — scelta: chiamata a identity con cache breve

Il gateway estrae la credenziale (`Authorization: Bearer gst_...` oppure cookie `gst_session`) e chiama `POST /internal/verify`. L'esito è tenuto in una cache in memoria del gateway.

Flusso:

1. Nessuna credenziale → il gateway lascia passare solo le rotte pubbliche (`security: []`: login, provider OIDC, health); altrimenti 401 `unauthenticated`.
2. La chiave di cache è lo SHA-256 della credenziale (mai il valore in chiaro in memoria o nei log).
3. Cache hit valido → il gateway usa il `Principal` in cache.
4. Cache miss → `POST /internal/verify {credential, kind}`. `active: false` → 401. `active: true` → il gateway mette in cache il principal per `cacheTtlSeconds` e inoltra la richiesta a valle con gli header `X-Gitstack-User-Id`, `X-Gitstack-Username`, `X-Gitstack-Scopes`, dopo aver **eliminato** ogni analogo header ricevuto dal client (i servizi a valle si fidano di questi header solo perché arrivano dal gateway).
5. TTL: **30 s** per gli esiti positivi (mai oltre `expiresAt` della credenziale), **5 s** per i negativi (limita il martellamento con credenziali inventate). Valori di partenza, configurabili nel gateway (GIT-38).
6. Se identity non risponde: il gateway serve dalla cache quel che ha, ma **non** allunga il TTL; per il resto risponde 503 (mai "fail open").

Perché non la verifica locale:

- I token `gst_` sono **opachi e revocabili** (D5): la revoca di un token, la disattivazione di un utente o il cambio di password devono avere effetto. Con un JWT firmato verificato localmente servirebbe comunque una lista di revoca o una scadenza brevissima, cioè di nuovo una chiamata a identity, più la gestione delle chiavi di firma nel gateway.
- Il gateway resterebbe senza database e senza segreti di dominio: identity è l'unico a conoscere gli hash; una compromissione del gateway non espone credenziali verificabili.
- Il costo è una chiamata interna per credenziale ogni 30 s, trascurabile per l'ordine di grandezza di un'installazione on-prem; la cache toglie identity dal percorso caldo.

Compromesso accettato: dopo una revoca il gateway può accettare la vecchia credenziale fino a 30 s (il TTL della cache). Identity la rifiuta subito. Se serve una revoca immediata, si aggiunge senza cambiare il contratto un evento sul bus (D4) `identity.credential.revoked` che il gateway usa per svuotare la voce; non è nell'ambito di M-02/A.

Le sessioni sono cookie opachi `gst_session` (HttpOnly, Secure, SameSite=Lax) con scadenza assoluta e `last_seen_at`; sono verificate con lo stesso `/internal/verify` dei token.

## Modello dati (schema Postgres `identity`)

Migrazioni in `internal/migrate/sql` (embed, golang-migrate, come `services/core/internal/migrate`); lo schema lo crea `EnsureSchema`, non le migrazioni, così funziona con il ruolo a permessi limitati di `migrations/bootstrap-role.sql` (ruolo `identity_app`, proprietario del solo schema `identity`).

| Migrazione | Tabelle |
|---|---|
| `0001_init_identity_schema` | `users`, `credentials`, `sessions`, `api_tokens`, `ssh_keys`, `oidc_providers`, `oidc_identities` |
| `0002_orgs_teams_grants` | `organizations`, `org_members`, `teams`, `team_members`, `resource_grants` |

Nessun segreto in chiaro:

- `credentials.secret_hash`: stringa PHC argon2id (salt e parametri inclusi).
- `sessions.token_hash`, `api_tokens.token_hash`: SHA-256 (32 byte, imposti da CHECK: un valore in chiaro non passa); indice unico. Il token `gst_...` compare solo nella risposta di creazione. `token_hint` = ultimi 4 caratteri.
- `oidc_providers.client_secret_enc`: cifrato con AES-256-GCM dall'applicazione (nonce + ciphertext), `enc_key_id` per la rotazione; la chiave sta in un Secret k8s, non nel database.
- `ssh_keys`: chiavi pubbliche (non segrete); `fingerprint_sha256` unico.

Vincoli notevoli: username/nomi minuscoli con CHECK di formato; email unica case-insensitive; `team_members` ha una FK composta verso `org_members`, quindi un membro di team è sempre membro dell'organizzazione e uscire dall'org lo toglie dai team; `resource_grants` ha esattamente un soggetto (utente **o** team, CHECK `num_nonnulls = 1`), un grant per (risorsa, soggetto), e **nessuna FK** su `resource_id` perché la risorsa vive nello schema `core` (D6).

Aperto per gli item successivi: nessuna API di amministrazione dei provider OIDC (oggi righe di `oidc_providers` inserite da configurazione/operatore); gli stati di `state`/`nonce` del login OIDC non sono a database (cookie firmato, decisione di GIT-3x).

## Configurazione

Tutta la configurazione da variabili d'ambiente, prefisso `GITSTACK_IDENTITY_`.
Nessun file di configurazione: il binario è pensato per container Docker e
deployment Kubernetes.

| Variabile | Default | Obbligatoria | Descrizione |
|---|---|---|---|
| `GITSTACK_IDENTITY_ADDR` | `:8080` | no | Indirizzo di ascolto HTTP (es. `:8080`, `0.0.0.0:8080`). |
| `GITSTACK_IDENTITY_DB_URL` | *(nessuno)* | sì | Stringa di connessione Postgres (es. `postgres://identity:***@postgres:5432/gitstack?sslmode=disable`). |
| `GITSTACK_IDENTITY_DB_MAX_CONNS` | `10` | no | Numero massimo di connessioni nel pool verso Postgres. |
| `GITSTACK_IDENTITY_MIGRATIONS_TIMEOUT` | `30s` | no | Tempo massimo per l'applicazione delle migrazioni all'avvio. |
| `GITSTACK_IDENTITY_LOG_LEVEL` | `info` | no | Livello minimo dei log strutturati (`debug`, `info`, `warn`, `error`). |

## Utilizzo

Il binario supporta tre modalità di esecuzione:

- `identity` oppure `identity serve`: applica le migrazioni non ancora applicate
  e avvia il server HTTP. È il comportamento di default nel container.
- `identity migrate up`: applica tutte le migrazioni non ancora applicate ed
  esce. Utile per un Job Kubernetes separato dalle migrazioni.
- `identity migrate down [N]`: applica il rollback delle ultime N migrazioni
  (default 1 step). Pensato per lo sviluppo e la CI.

## Probe di salute

Il server HTTP espone due endpoint per i probe Kubernetes (non versionati):

- `GET /healthz`: risponde sempre `200 OK` finché il processo è vivo
  (liveness probe). Non verifica dipendenze esterne.
- `GET /readyz`: risponde `200 OK` se Postgres è raggiungibile entro
  3 secondi, altrimenti `503 Service Unavailable` (readiness probe).

## Note di sicurezza

La stringa di connessione Postgres contiene la password. Il servizio la usa per
aprire il pool di connessioni e per le migrazioni, ma **non la logga mai**:
nessun log contiene né la password né la stringa di connessione. I log di errore
di avvio indicano host e database, mai credenziali.
