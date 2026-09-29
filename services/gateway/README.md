# gateway

Unico punto d'ingresso API di GitStack: routing verso gli altri servizi, verifica dei token, rate limiting. Dietro Traefik.

## M-01/T-04 (questo task)

In M-01 il gateway fa solo routing e osservabilità: verifica del token e rate limiting arrivano in M-02 (qui c'è solo il punto di aggancio, no-op).

- `GET /healthz`, `GET /readyz`: probe di liveness/readiness (convenzione k8s), non fanno parte del contratto pubblico `api/openapi.yaml`. Il corpo riusa comunque lo schema `Health` del contratto, per restare coerente. `/healthz` risponde sempre `ok` (il processo è vivo); `/readyz` risponde `ok` solo se riesce a raggiungere `core` in tempo utile.
- `/v1/*`: le operazioni del contratto OpenAPI, generate in `internal/openapi` da `api/openapi.yaml` (vedi `scripts/generate-api.sh`, non modificare a mano). Il gateway non implementa logica di business: ogni operazione instrada la richiesta verso `core` (proxy generico, `internal/proxy`), rimuovendo il prefisso `/v1`. Fanno eccezione le rotte dei tag `auth`, `users`, `tokens`, `ssh-keys`, `organizations`, `teams` e `permissions` (`identityPatterns` in `internal/httpserver/router.go`): vanno a `identity` (`GITSTACK_IDENTITY_URL`, errore `identity_unavailable` se non risponde). Il test `TestIdentityRoutes_SecondoIlContratto` le confronta con `api/openapi.yaml`; `/v1/internal/*` (tag `internal`) non è mai instradato (404).
- Catena di middleware (`internal/middleware`): request id (letto da `X-Request-Id` se presente, altrimenti generato; propagato a valle e in risposta), log strutturati JSON, e i punti di aggancio no-op `Auth`/`RateLimit` che M-02 sostituirà con la verifica reale.

### Configurazione (variabili d'ambiente)

| Variabile | Obbligatoria | Default | Descrizione |
|---|---|---|---|
| `GITSTACK_GATEWAY_ADDR` | no | `:8080` | Indirizzo di ascolto HTTP |
| `GITSTACK_CORE_URL` | sì | — | Base URL di `core` (es. `http://core:8080`); il chart Helm (GIT-8) la imposta |
| `GITSTACK_CORE_TIMEOUT` | no | `5s` | Timeout per le richieste instradate verso `core` (formato `time.Duration` di Go) |
| `GITSTACK_IDENTITY_URL` | no | — | Base URL di `identity` (es. `http://identity:8080`); il chart Helm (GIT-36) la imposta. Senza, le rotte di identity non sono montate (404) |
| `GITSTACK_IDENTITY_TIMEOUT` | no | `5s` | Timeout per le richieste instradate verso `identity` |
| `GITSTACK_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` o `error` |
| `GITSTACK_GATEWAY_TRUSTED_PROXIES` | no | vuoto | CIDR separati da virgola dei proxy davanti al gateway (es. la rete di Traefik, `10.42.0.0/16`). Solo da questi peer si legge `X-Forwarded-For` per l'IP del client (ultimo indirizzo non fidato da destra); vuoto = nessuno fidato, l'IP è quello della connessione e `X-Forwarded-For` di un peer non fidato è ignorato |

### Sviluppo locale

```
go work sync
cd services/gateway
go generate ./...   # rigenera internal/openapi da api/openapi.yaml (solitario via scripts/generate-api.sh)
go build ./...
go test ./...
```

### Immagine Docker

`Dockerfile` multi-stage (build Go + distroless statico). Il contesto di build è questa cartella, non la radice del monorepo:

```
docker build -f services/gateway/Dockerfile services/gateway
```

Il tag e il push nel registry interno sono compito del job `registry` (GIT-2), non di questo Dockerfile.
