# gateway

Unico punto d'ingresso API di GitStack: routing verso gli altri servizi, autenticazione centralizzata (sessione e token), scope per rotta, rate limiting (ancora un punto di aggancio). Dietro Traefik.

## M-01/T-04: routing e osservabilità

In M-01 il gateway fa routing e osservabilità; l'autenticazione (GIT-54) è descritta più sotto, il rate limiting resta un punto di aggancio no-op.

- `GET /healthz`, `GET /readyz`: probe di liveness/readiness (convenzione k8s), non fanno parte del contratto pubblico `api/openapi.yaml`. Il corpo riusa comunque lo schema `Health` del contratto, per restare coerente. `/healthz` risponde sempre `ok` (il processo è vivo); `/readyz` risponde `ok` solo se riesce a raggiungere `core` in tempo utile.
- `/v1/*`: le operazioni del contratto OpenAPI, generate in `internal/openapi` da `api/openapi.yaml` (vedi `scripts/generate-api.sh`, non modificare a mano). Il gateway non implementa logica di business: ogni operazione instrada la richiesta verso `core` (proxy generico, `internal/proxy`), rimuovendo il prefisso `/v1`. Fanno eccezione le rotte dei tag `auth`, `users`, `tokens`, `ssh-keys`, `organizations`, `teams` e `permissions` (`identityPatterns` in `internal/httpserver/router.go`): vanno a `identity` (`GITSTACK_IDENTITY_URL`, errore `identity_unavailable` se non risponde). Il test `TestIdentityRoutes_SecondoIlContratto` le confronta con `api/openapi.yaml`; `/v1/internal/*` (tag `internal`) non è mai instradato (404).
- Catena di middleware (`internal/middleware`): request id (letto da `X-Request-Id` se presente, altrimenti generato; propagato a valle e in risposta), log strutturati JSON, `RateLimit` (aggancio no-op) e `Auth` (l'autenticazione reale, vedi sotto). Ordine dall'esterno: request id, log, `Auth`, `RateLimit`, gestore.

## Autenticazione centralizzata (GIT-54, M-02/J1)

Il progetto è in `services/identity/README.md` ("Come il gateway verifica sessioni e token"); qui com'è realizzato.

**Dichiarazione di sicurezza per rotta.** `internal/security/routes.gen.go` è generato da `api/openapi.yaml` (`go generate ./internal/security`, lanciato da `scripts/generate-api.sh`; la CI lo rigenera e confronta): per ogni operazione non interna dice se è pubblica (`security: []`: login, provider OIDC, `/health`) o autenticata, con quali credenziali (sessione, token, o entrambe: `POST /auth/logout` è solo sessione), con quali scope (`x-required-scopes`) se resta raggiungibile con la password da cambiare (`x-password-change-exempt`) e, per le rotte di core su una risorsa (`{resourceId}`), il ruolo minimo richiesto sulla risorsa (`x-required-permission`: `read`, `write` o `admin`; `GET` read, `PATCH` write, `DELETE` admin). Il generatore fallisce se una rotta non pubblica di core con `{resourceId}` non lo dichiara (niente rotte aperte per dimenticanza), o se è dichiarato dove il gateway non lo applica; le rotte di identity su `{resourceId}` (grant, permesso effettivo) applicano il ruolo `admin` dentro identity.

**Permesso su risorsa.** Dopo l'autenticazione e il controllo degli scope, il middleware `Auth` chiama `POST /internal/permissions/check` di identity (`identityclient.Client.CheckPermission`, stesso segreto di servizio; senza cache, così un grant revocato vale subito). `allowed: false` → 403 `forbidden`, senza dati della risorsa e identico se la risorsa non esiste (core non viene interpellato: nessun 404 che ne riveli l'esistenza); identity che non risponde o risponde in modo inatteso → 503 `identity_unavailable` (mai fail open). L'ordine degli errori è 401, 503, 403 `password_change_required`, 403 `insufficient_scope`, 403 `forbidden`. Test: `internal/httpserver/permissions_test.go` (a tabella) e `services/core/internal/stackitest/permissions_integration_test.go` (gateway + identity + core). Una richiesta a `/v1/*` senza dichiarazione (metodo o percorso non del contratto, anche sotto i pattern `{rest...}` di `identityPatterns`) risponde 404 e non arriva a valle. `TestSicurezza_OgniRottaHaLaSuaDichiarazione` fallisce se una rotta del contratto o un pattern di `identityPatterns` non ha la sua dichiarazione. `/healthz` e `/readyz` sono probe fuori dal contratto e non passano da `Auth`.

**Flusso di `middleware.Auth`.**

1. Rotta pubblica: passa senza credenziali.
2. Credenziale: `Authorization: Bearer gst_...` (token) oppure cookie `gst_session` (sessione). Assente, non ammessa dalla rotta o non attiva → 401 `unauthenticated`.
3. Verifica con `POST /internal/verify` di identity (`internal/identityclient`, tipi generati dal contratto in `types.gen.go`; Bearer col segreto di servizio). Identity irraggiungibile, con errore o con risposta incoerente → 503 `identity_unavailable`, mai fail open.
4. Sessione con `mustChangePassword` (admin del primo avvio): 403 `password_change_required` a ogni rotta, tranne `GET /auth/session`, `POST /auth/logout` e `PUT /users/{username}/password` sulla propria utenza. Il cambio password toglie la voce dalla cache, così la sessione si sblocca subito; lo stesso fa `POST /auth/logout` (`Forget` anche se identity risponde 204), così la sessione revocata riceve 401 subito e non dopo il TTL di 30 s. Le operazioni `/resources` richiedono `read:resource` (letture) o `write:resource` (scritture).
5. Token: deve avere gli scope della rotta (`write:*` include `read:*` dello stesso ambito, `admin:org` include `write:org`); altrimenti 403 `insufficient_scope` con `details.required`. Le sessioni non hanno scope e passano.

**Cache** (`identityclient.Cache`, come da README di identity): chiave = SHA-256 della credenziale (il valore in chiaro non resta in memoria né nei log); esiti positivi 30 s (`GITSTACK_GATEWAY_AUTH_CACHE_TTL`), mai oltre `expiresAt` e mai oltre il `cacheTtlSeconds` di identity se più breve; negativi 5 s (`..._NEGATIVE_TTL`); massimo 10000 voci. Con identity giù si serve solo ciò che è ancora nel TTL, senza allungarlo; il resto è 503. Compromesso noto: dopo una revoca il gateway può accettare la vecchia credenziale fino a un TTL (identity la rifiuta subito).

**Identità verso i servizi a valle** (`internal/trust`). Il gateway cancella ogni header `X-Gitstack-*` ricevuto dal client e, per le richieste autenticate, scrive `X-Gitstack-User-Id`, `X-Gitstack-Username`, `X-Gitstack-Scopes` (vuoto per le sessioni), `X-Gitstack-Token-Id` e `X-Gitstack-Token-Name` (id e nome del token usato, vuoti per le sessioni; il nome è ripulito da caratteri di controllo e spazi ai bordi), `X-Gitstack-Timestamp` e `X-Gitstack-Signature` = HMAC-SHA256 con il segreto di servizio su `gitstack-identity-v1\n<timestamp>\n<userId>\n<username>\n<scopes>\n<tokenId>\n<tokenName>`. Core (`services/core/internal/trust`) accetta l'identità solo con firma valida e timestamp entro 60 s, altrimenti 401: chi lo raggiunge direttamente non può spacciarsi per un utente senza conoscere il segreto. A core non arrivano `Authorization` e cookie del client; a identity sì. Identity autentica il cookie da sé e, senza cookie, accetta gli stessi header firmati (`services/identity/internal/trust`): così un token con gli scope giusti funziona anche su `/users`, `/orgs`, ...

**Limiti noti.** Il modello dei permessi (chi può cosa su quale risorsa) è di GIT-38; il gateway applica solo gli scope.

### Configurazione (variabili d'ambiente)

| Variabile | Obbligatoria | Default | Descrizione |
|---|---|---|---|
| `GITSTACK_GATEWAY_ADDR` | no | `:8080` | Indirizzo di ascolto HTTP |
| `GITSTACK_CORE_URL` | sì | — | Base URL di `core` (es. `http://core:8080`); il chart Helm (GIT-8) la imposta |
| `GITSTACK_CORE_TIMEOUT` | no | `5s` | Timeout per le richieste instradate verso `core` (formato `time.Duration` di Go) |
| `GITSTACK_IDENTITY_URL` | no | — | Base URL di `identity` (es. `http://identity:8080`); il chart Helm (GIT-36) la imposta. Senza, le rotte di identity non sono montate (404) e ogni rotta autenticata risponde 503 (nessuna verifica possibile, mai fail open) |
| `GITSTACK_IDENTITY_TIMEOUT` | no | `5s` | Timeout per le richieste instradate verso `identity` |
| `GITSTACK_IDENTITY_SERVICE_SECRET` | sì, se c'è `GITSTACK_IDENTITY_URL` | — | Segreto di servizio (Secret `<release>-identity-service`, chiave `secret`): Bearer verso `/internal/verify` e chiave della firma dell'identità inoltrata a core. Mai nei log; il chart lo monta con `secretKeyRef` |
| `GITSTACK_GATEWAY_AUTH_CACHE_TTL` | no | `30s` | Durata in cache degli esiti positivi di verifica (mai oltre `expiresAt`) |
| `GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL` | no | `5s` | Durata in cache degli esiti negativi |
| `GITSTACK_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` o `error` |
| `GITSTACK_GATEWAY_TRUSTED_PROXIES` | no | vuoto | CIDR separati da virgola dei proxy davanti al gateway (es. la rete di Traefik, `10.42.0.0/16`). Solo da questi peer si legge `X-Forwarded-For` per l'IP del client (ultimo indirizzo non fidato da destra); vuoto = nessuno fidato, l'IP è quello della connessione e `X-Forwarded-For` di un peer non fidato è ignorato  Il chart lo imposta da `gateway.env.trustedProxies` (default `10.42.0.0/16`). Solo da questi peer il gateway conserva anche `X-Forwarded-Proto` (`http` o `https`) invece di sovrascriverlo con lo schema della propria connessione: identity lo usa per marcare `Secure` il cookie di sessione solo su HTTPS (GIT-153) |

### Sviluppo locale

```
go work sync
cd services/gateway
go generate ./...   # rigenera internal/openapi da api/openapi.yaml (solitario via scripts/generate-api.sh)
go build ./...
go test ./...
```

### Immagine Docker

`Dockerfile` multi-stage (build Go + distroless statico). Il contesto di build è la radice del monorepo (GIT-184):

```
docker build -f services/gateway/Dockerfile .
```

Il tag e il push nel registry interno sono compito del job `registry` (GIT-2), non di questo Dockerfile.
