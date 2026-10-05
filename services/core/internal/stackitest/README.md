# stackitest — test d'integrazione sullo stack completo

Gateway e identity sono i binari veri (compilati con `go build`), core è il router
vero in-process, il database è un Postgres reale (`internal/dbtest`). I file hanno il
build tag `integration`; in CI girano a ogni push su `main` e sulle PR (job `go` di
`ci.yml`, passo `go test -tags=integration ./... -race` in `services/core`).

Locale: `cd services/core && go test -tags=integration -count=1 ./internal/stackitest/`
(serve Docker; per `TestGitClientReale` anche `git`, `ssh` e `ssh-keygen` nel PATH, altrimenti
il test è saltato). Su Windows: `DOCKER_HOST=npipe:////./pipe/dockerDesktopLinuxEngine` e
`TESTCONTAINERS_RYUK_DISABLED=true`.

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
| `token_troppo_lungo` | Bearer e cookie oltre 512 caratteri: 401 senza chiamare identity (GIT-60); anche a 300 caratteri (sotto il limite) 401 |
| `risorsa_di_altra_organizzazione` | utente di org B senza grant su una risorsa di org A: GET, PATCH, DELETE e `GET /grants` danno 403 `forbidden` (token e sessione web); `GET /resources` non la elenca e `total` è 0; la risorsa resta intatta. Sanità: chi ha il grant la vede |

## Client git reale (`git_integration_test.go`, `TestGitClientReale`, GIT-77)

Lo stack è completo: gateway, identity e **servizio git** sono binari veri (compilati con
`go build`), core è il router vero in-process con il client dell'API interna di git e
l'indirizzo di clone configurato, Postgres è reale. Il client è il `git` del PATH, con `ssh` vero
(chiave ed25519 generata con `ssh-keygen`, registrata via `POST /user/ssh-keys`) sulla porta SSH
del servizio. HOME isolata, nessun credential helper, nessun prompt; le variabili `GIT_CONFIG_*`
dell'ambiente si tolgono. Utenti: `alice` (proprietaria), `bob` (token e chiave, nessun grant),
`carol` (chiave **non** registrata). Ogni caso negativo controlla che l'output di git e il corpo HTTP
non contengano il nome del file riservato, il suo contenuto, lo sha del commit (intero e abbreviato)
né l'id del repo (`leakCheck`); dove serve controlla anche che `refs/heads/main` sul server non sia cambiato.

| Sottotest | Cosa prova |
| --- | --- |
| `https_clone_push_pull` | repo creato via API (`POST /repos`), push iniziale, clone, push e pull con token in Basic auth; `cloneUrls` del repo coerenti con la configurazione |
| `ssh_clone_push_pull` | clone, push e pull via SSH con la chiave registrata; incrocio con HTTPS nei due versi |
| `senza_credenziali` | HTTPS: 401 con `WWW-Authenticate: Basic`, token inventato 401, password al posto del token 401, clone anonimo fallito; SSH con chiave non registrata rifiutato; nessun dato del repo |
| `utente_senza_permesso` | bob su repo privato: clone e push HTTPS, clone SSH rifiutati; `info/refs` dà 404 identico a un repo inesistente; `GET /repos` 404; nessun dato; repo intatto |
| `token_di_sola_lettura` | token `read:resource` clona ma il push dà 403, repo invariato |
| `repo_eliminato` | dopo `DELETE /repos` clone HTTPS/SSH, push e `GET /repos` danno 404 senza dati del repo |
| `repo_interno_leggibile_non_scrivibile` | repo `internal`: bob lo clona via HTTPS e SSH e lo legge via API, ma il push (HTTPS 403, SSH) è negato e il repo non cambia; chiave non registrata esclusa |
| `R6_file_oltre_100MB` | file di 100 MB + 1 byte generato al volo: push rifiutato via HTTPS e SSH (`remote: gitstack: push rifiutato`, nome e byte), repo invariato; tolto il file il push passa |
| `R9_branch_principale_protetto` | force-push ed eliminazione di `main` rifiutati via HTTPS e SSH; altri branch liberi; con `protectDefaultBranch=false` il force-push passa |
| `evento_git_push_su_nats` | nats-server JetStream in-process; dopo un push HTTPS e uno SSH lo stream `GIT` ha due eventi `git.push` decodificati con `gitpush.Register`/`Registry.Decode` (repo, pusher, ref, before/after, commit); un push negato non pubblica niente |
| `R10_repo_archiviato` | archiviato: si clona (HTTPS e SSH), il push è negato (403 / SSH) e il repo non cambia; riattivato si scrive di nuovo |

## VM di test

`deploy/test-vm/e2e.ps1` ha il passo e5: utente di prova con token e chiave SSH, repo creato via API, push e clone
via HTTPS (ingress) e via SSH sulla porta 2222 della VM, pull incrociato e clone anonimo negato. Vedi
`deploy/test-vm/README.md`.
