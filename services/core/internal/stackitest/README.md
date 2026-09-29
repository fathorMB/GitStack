# stackitest — test d'integrazione sullo stack completo

Gateway e identity sono i binari veri (compilati con `go build`), core è il router
vero in-process, il database è un Postgres reale (`internal/dbtest`). I file hanno il
build tag `integration`; in CI girano a ogni push su `main` e sulle PR (job `go` di
`ci.yml`, passo `go test -tags=integration ./... -race` in `services/core`).

Locale: `cd services/core && go test -tags=integration -count=1 ./internal/stackitest/`
(serve Docker).

## Test di sicurezza (`security_integration_test.go`, `TestSecurity`)

Il gateway gira con cache di verifica di 1 s, così revoca, logout e cambio password si
provano senza attese lunghe. Il login ha soglia 5 fallimenti per utente. Ogni caso
negativo controlla anche che il corpo della risposta non contenga id o nome della
risorsa creata nel setup (`noLeak`).

| Sottotest | Cosa prova |
| --- | --- |
| `nessuna_credenziale` | 401 `unauthenticated` su rotte di risorse, utenti, sessione e token; Authorization vuoto o con schema sconosciuto; `/internal/*` non esposto (404) |
| `token_malformato` | token e cookie inventati, senza prefisso, con caratteri strani: 401 |
| `token_revocato` | `DELETE /user/tokens/{id}`, dopo il TTL della cache il token dà 401 |
| `token_scaduto` | `expires_at` portato nel passato con UPDATE su `identity.api_tokens`: 401 |
| `scope_insufficiente` | `read:user` su `/resources`, `read:resource` in scrittura, su `/orgs` e `/users`: 403 `insufficient_scope` |
| `sessione_dopo_logout` | la sessione dopo `POST /auth/logout` dà 401 |
| `sessione_dopo_cambio_password` | dopo il cambio password le altre sessioni sono revocate, la vecchia password non entra più |
| `header_identita_falsificati` | `X-Gitstack-*` al gateway ignorati (anche insieme a un token con pochi scope); a core direttamente: assenti, inventati, firma con altro segreto, firma scaduta, header modificato dopo la firma |
| `brute_force_login` | dopo 5 fallimenti 429 `too_many_attempts` anche con la password giusta, senza cookie; altri utenti non toccati; utente inesistente trattato come uno esistente |
| `token_troppo_lungo` | **saltato** (difetto noto): una credenziale di 512 caratteri fa rispondere 503 `identity_unavailable` invece di 401 |

## Da aggiungere

- Permesso mancante su una risorsa di un'altra organizzazione: dipende da GIT-38
  (grant e `/internal/permissions/check`).
