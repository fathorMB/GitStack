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

### Provare le migrazioni su un Postgres vero

```
docker run -d --rm --name identity-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=gitstack -p 55432:5432 postgres:16-alpine
cd services/identity
GITSTACK_TEST_DATABASE_URL="postgres://postgres:pw@localhost:55432/gitstack?sslmode=disable" \
  go test -tags=integration -count=1 -v ./internal/migrate/
```

Il test applica up (due volte: idempotenza), verifica le tabelle, applica down di 2 passi, riapplica up, e prova i vincoli (unicità, CHECK sugli hash, scope sconosciuti, FK composta team/org, grant con due soggetti). Senza la variabile viene saltato. Per provare con il ruolo a permessi limitati: `psql -v identity_password=... -f migrations/bootstrap-role.sql` e usare `identity_app` nel DSN.

## Utenti locali, password e sessioni web (GIT-33, M-02/E)

Fase 1 (senza handler HTTP: main, config e router sono di GIT-30). Pacchetti in `internal/`:

| Pacchetto | Cosa fa |
|---|---|
| `password` | hash argon2id in formato PHC, verifica a tempo costante, politica, hash finto per il login |
| `users` | creazione, lettura, elenco, aggiornamento, disattivazione, eliminazione, cambio password; protezione dell'ultimo amministratore |
| `sessions` | sessioni web su `identity.sessions` (solo SHA-256 del cookie), scadenza assoluta, revoca |
| `loginlimit` | rate limit dei login falliti per utente e per IP, con clock iniettato |
| `auth` | `Login`, `Logout`, `Session` e i cookie `gst_session` (`SessionCookie`, `ClearCookie`) |
| `dbtest` | Postgres reale per i test d'integrazione (tag `integration`) |

### Password (argon2id)

Formato salvato in `credentials.secret_hash`: `$argon2id$v=19$m=<KiB>,t=<passate>,p=<thread>$<salt>$<hash>` (base64 senza padding). Parametri costanti (OWASP: 19 MiB, 2 passate, 1 thread): `m=19456,t=2,p=1`, salt 16 byte da `crypto/rand`, chiave 32 byte. Parametri e salt stanno nell'hash: cambiare le costanti in `password` non invalida gli hash esistenti, che al primo login riuscito vengono riscritti con i parametri nuovi (`NeedsRehash`). Confronto con `subtle.ConstantTimeCompare`; i parametri letti da un hash salvato hanno un tetto (memoria ≤ 1 GiB, t ≤ 64).

Politica minima: 12–1024 caratteri (come il contratto), non solo spazi, diversa da username ed email.

### Login e sessioni

- Utente inesistente, password errata, utente disattivato e utente senza password danno lo stesso `ErrInvalidCredentials` (401 `invalid_credentials`) e fanno tutti una verifica argon2id (contro un hash finto per l'inesistente), quindi i tempi restano simili.
- Cookie `gst_session`: 32 byte casuali in base64url; `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`. Nel database c'è solo lo SHA-256 grezzo (`tokens.HashBytes`). Durata assoluta 7 giorni (`sessions.DefaultTTL`); `last_seen_at` si aggiorna al più una volta al minuto.
- Logout: revoca la sessione corrente. Cambio password: revoca tutte le sessioni dell'utente, oppure tutte tranne quella del chiamante (`KeepSessionID`, il contratto dice "le altre sessioni"). Disattivazione: revoca tutte le sessioni e tutti i token personali. Tutto nella stessa transazione della modifica.
- Password, hash e valore del cookie non sono mai nei log né negli errori.

### Rate limit del login

Finestra scorrevole in memoria del processo (con più repliche il limite vale per replica). Ogni fallimento conta sulla chiave dell'utente (username/email in minuscolo, esista o no) e su quella dell'IP; a limite raggiunto `Login` risponde `*RateLimitedError{RetryAfter}` prima di toccare database e hash (il livello HTTP lo mapperà a 429 con `Retry-After`). Un login riuscito azzera il contatore dell'utente. Variabili (tutte opzionali):

| Variabile | Default | Significato |
|---|---|---|
| `GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER` | `5` | fallimenti per utente nella finestra |
| `GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_IP` | `20` | fallimenti per IP nella finestra |
| `GITSTACK_IDENTITY_LOGIN_WINDOW` | `15m` | durata della finestra (formato Go) |

`loginlimit.FromEnv` le legge; il collegamento a `internal/config` è della fase 2.

### Test d'integrazione

```
docker run -d --rm --name identity-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=gitstack -p 55432:5432 postgres:16-alpine
cd services/identity
GITSTACK_TEST_DATABASE_URL="postgres://postgres:pw@localhost:55432/gitstack?sslmode=disable" \
  go test -tags=integration -count=1 ./...
```

`dbtest` crea un database per test (`CREATE DATABASE`), quindi i pacchetti girano in parallelo; se il ruolo non può creare database ripiega sul database indicato (allora `-p 1`). Senza la variabile i test d'integrazione sono saltati. Il blocco dopo N tentativi e lo sblocco dopo la finestra (clock finto, senza sleep) sono in `auth/auth_integration_test.go`.

### Livello HTTP (GIT-33, fase 2)

`internal/openapi` contiene la `ServerInterface` generata da `api/openapi.yaml` per i tag `auth` e `users` (`include-tags` in `oapi-codegen.yaml`; si rigenera con `scripts/generate-api.sh`). `internal/httpapi` la implementa: `httpapi.New(*auth.Service, *slog.Logger) http.Handler` è pronto da montare (percorsi senza `/v1`, come core: il prefisso lo toglie il gateway). Il collegamento a `main.go` e a `internal/config` (compresa la lettura di `loginlimit.FromEnv`) è di GIT-30.

- Autenticazione: solo cookie `gst_session` (i token personali sono di un altro item). Senza sessione valida 401 `unauthenticated`.
- Permessi: creare ed eliminare utenti solo admin (403 `forbidden` altrimenti); aggiornare profilo e cambiare password admin o l'utente stesso (chi non è admin né l'utente stesso non scopre nemmeno se l'utente esiste: 403); `isAdmin`/`isActive` solo admin; leggere utenti qualunque utente autenticato (email e campi amministrativi solo per sé e per gli admin).
- Login: 200 con `CurrentSession` e `Set-Cookie`; 401 `invalid_credentials` identico (stesso status e corpo) per password errata, utente inesistente e utente disattivato; 429 `too_many_attempts` con `Retry-After` in secondi (per eccesso). L'IP del rate limit è `r.RemoteAddr` senza porta, mai `X-Forwarded-For`; se non determinabile vale solo il limite per utente. Dietro il gateway `RemoteAddr` è l'indirizzo del gateway: per un limite per client reale servirà un meccanismo fidato dal gateway (da decidere).
- `changePassword`: password attuale sbagliata → 403 `forbidden` (non 401: il chiamante è già autenticato); revoca le altre sessioni (l'admin che agisce su un altro utente le revoca tutte).
- Errori nel formato `Error`; 400 `bad_request` per JSON malformato/campi sconosciuti/parametri fuori range, 422 `validation_failed` con `details.fields`, 409 `already_exists`/`last_admin`, 500 senza dettagli. Le operazioni OIDC rispondono 501 `not_implemented` (altro item).

## Token personali, chiavi SSH e interfaccia interna (GIT-34, M-02/F)

Pacchetti: `internal/apitokens` (token `gst_...`), `internal/userkeys` (chiavi SSH); gli handler stanno in `internal/httpapi/tokens_keys.go`. `httpapi.New` prende opzioni: `WithTokens`, `WithSSHKeys`, `WithServiceSecret`; senza `WithTokens`/`WithSSHKeys` le relative operazioni rispondono 501. L'ambiente (segreto, massimo di durata) lo legge chi monta il server, non questi pacchetti.

- **Token**: nome (1-64), scope dal catalogo (`tokens.ParseScopes`), `expiresAt` **obbligatoria**, nel futuro e non oltre il massimo (parametro di `apitokens.New`, default 365 giorni; altrimenti 422). Nel database c'è solo `tokens.HashBytes` (BYTEA di 32 byte) e l'hint di 4 caratteri; il valore in chiaro è solo nella risposta 201 di `POST /user/tokens`. Elenco senza revocati; il nome di un token revocato si può riusare. Nome duplicato: 409 `already_exists`.
- **Chiavi SSH**: parsing solo con `sshkeys.Parse`. Fingerprint già registrato (da chiunque, anche dallo stesso utente): 409 `ssh_key_in_use` (indice `ssh_keys_fingerprint_key`); titolo già usato dallo stesso utente: 409 `already_exists` con `field: title` (indice `ssh_keys_user_title_key`). Chiave di un altro utente in GET/DELETE: 404.
- **`POST /internal/verify`**: accetta cookie di sessione o `gst_...` (o `kind`); sconosciuto, scaduto, revocato, utente disattivato → `active: false` senza distinzione (cache 5 s; 30 s se attivo). `last_used_at` si aggiorna al più una volta al minuto. **`GET /internal/ssh-keys/{fingerprint}`** (URL-encoded) risolve l'utente per il servizio git. `POST /internal/permissions/check` resta 501 (item dei permessi).
- **serviceAuth**: middleware su `/internal/*`, `Authorization: Bearer <segreto>` confrontato in tempo costante; segreto vuoto = tutto 401.
- Le operazioni `/user/*` accettano oggi solo il cookie di sessione: l'autenticazione con token (e il controllo degli scope) è del gateway.
- Test: `go test -tags integration ./internal/apitokens ./internal/userkeys ./internal/httpapi` con `GITSTACK_TEST_DATABASE_URL`.
