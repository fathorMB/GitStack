<!-- lmbrain:begin -->
# LMBrain Lite Agent Instructions

Read `.lmbrain-lite/AGENT.md` first: it is the operating contract for the lead agent in this workspace. Start every session with the `lite_digest` MCP tool. Track work with `lite_task_set` and `lite_log`. Never edit `.lmbrain-lite/ROADMAP.md` by hand: it is generated from `.lmbrain-lite/milestones/`.
<!-- lmbrain:end -->


<!-- prisma:begin -->
# Prisma documentation companion
For Prisma knowledge consultation and development notes, read `.prisma/CODING.md`. Keep following this project's existing coding instructions and skills. Use only the Prisma MCP coding role; do not edit Prisma knowledge or confirm analysis proposals.
<!-- prisma:end -->

# Sviluppo di GitStack dopo la tappa 2 di V7

Regole per gli agenti di coding che lavorano su questo repo dopo il passaggio della casa su GitStack (V7, tappa 2, condizione 5; fonte: `.prisma/knowledge/topics/rilascio-v1.md`). Vanno lette insieme alle regole sopra; non le sostituiscono.

Dalla tappa 2 parte il dogfooding della v1.0: almeno 2 settimane con nessuna perdita di dati, almeno un restore da un backup di homehub, almeno un upgrade rc.N → rc.N+1 su homehub con `gitstack upgrade` e nessun bug critico o alto aperto (registro in `docs/operations.md`).

## Dove si lavora

- **Casa del codice: homehub.** Gli agenti fanno push su homehub con `gs` e le skills (`skills/`), non su GitHub. GitHub è un mirror in push del ramo principale e dei tag (V8-mirror): **non si fa push diretto su GitHub**, e un commit lì creerebbe una divergenza (il mirror non forza mai, si ferma in `diverged`).
- Le credenziali per git si impostano con `gs auth setup-git`; i comandi `gs` leggono `GS_HOST` e `GS_TOKEN` (vedi `docs/agent-trial.md`). Il token non va mai scritto in file del repo, log o messaggi di commit.
- Le issues di sviluppo passano gradualmente da GalaxyLab a GitStack: lavora l'issue con le skills `gitstack-issue-to-commit` e `gitstack-notifications`.
- Un lavoro vive su un ramo (`agent/<n>-<slug>` per le issue di GitStack, `item/<chiave>` per gli item di GalaxyLab). Si spinge il ramo con `git push -u origin <ramo>`, si commenta la issue con il nome del ramo e **non si unisce su `main` senza il via della persona o del board**; niente push forzato, niente modifiche dirette a `main`.

## CI ponte: GitHub Actions solo su `main` e tag (scelta B)

Finché GitStack non ha una CI propria (v2-a, dopo la v1.0), `.github/workflows/ci.yml` parte **solo** su push a `main`, push dei tag `v*` e `pull_request`, e il mirror in push porta su GitHub soltanto `main` e i tag. Quindi:

- **Un ramo non viene mai verificato da GitHub Actions: i test si eseguono in locale prima di unire su `main`.** È la regola di sviluppo della scelta B; vale per ogni agente e per ogni persona.
- Rischio accettato: un errore che sfugge ai test locali arriva su `main` e lo segnala GitHub Actions dopo l'unione. In quel caso si apre subito una correzione su un ramo, mai un push forzato.
- La CI su GitHub è un secondo filtro, non il primo: «lo vedrà la CI» non è una ragione per saltare un controllo.

## Test locali, comando per comando

Sono gli stessi di `.galaxylab/checks.toml` (quelli che girano alla revisione) e di `.github/workflows/ci.yml`. Lancia quelli che riguardano i file che hai toccato, e **tutti** prima di unire su `main`. Prerequisiti: Go (versione di `go.work`), Node 22+ con corepack, `golangci-lint` v2.14.0 o più recente. Con `corepack` usa sempre `corepack pnpm ...` e mai `corepack use` (riscrive `package.json`).

### Go (dalla radice del repo)

Il repo è un workspace (`go.work`, nessun `go.mod` in radice): `go build ./...` dalla radice non funziona.

```sh
go work sync
go build ./admin/... ./api/... ./cli/... ./client/go/... ./pkg/egress/... ./pkg/events/... ./pkg/issuequery/... ./pkg/names/... ./services/core/... ./services/gateway/... ./services/git/... ./services/identity/...
go test ./admin/... ./api/... ./cli/... ./client/go/... ./pkg/egress/... ./pkg/events/... ./pkg/issuequery/... ./pkg/names/... ./services/core/... ./services/gateway/... ./services/git/... ./services/identity/...
```

La CI ripete build e test per modulo con `bash scripts/go-each.sh go test ./... -race`; per un solo modulo: `cd <modulo> && go test ./... -race`. I moduli sono quelli di `go list -m`: `admin`, `api`, `cli`, `client/go`, `pkg/egress`, `pkg/events`, `pkg/issuequery`, `pkg/names`, `services/core`, `services/gateway`, `services/git`, `services/identity`.

### Lint, per modulo toccato (da dentro il modulo)

```sh
cd <modulo> && golangci-lint run ./... --timeout=5m
```

Per tutti: `bash scripts/go-each.sh golangci-lint run ./... --timeout=5m`. golangci-lint non controlla gofmt: serve il passo seguente.

### Controlli di radice

```sh
go run scripts/check-gofmt.go                 # se cambi .go
go run scripts/check-rules-coverage.go        # se cambi docs/rules-coverage.md, services/** o web/src/**
go run scripts/check-security-md.go           # se cambi SECURITY.md
go run scripts/check-standalone-build.go      # se cambi go.mod/go.sum/Dockerfile dei servizi, pkg/**, go.work
go run scripts/check-internal-versions.go     # se cambi go.mod, Dockerfile, workflow, Makefile, go.work
```

Se tocchi `api/openapi.yaml`: `./scripts/generate-api.sh`, poi `go work sync` e `./scripts/check-api-generated.sh`, e committa tutti i file che cambiano.

### Test d'integrazione (come nella CI)

```sh
cd cli && go test -tags=integration -race -count=1 ./internal/cmd/auth/
cd admin && go test -tags=integration -race -count=1 -timeout 20m ./internal/pgcheck/ ./internal/backup/   # richiede Docker, GITSTACK_REQUIRE_DOCKER=1
cd services/core && go test -tags=integration ./... -race                                                  # richiede Docker (testcontainers) e node
cd services/identity && go test -tags=integration -race -count=1 ./...                                     # richiede GITSTACK_TEST_DATABASE_URL
```

Se non hai Docker, dichiaralo in revisione: non sono controlli saltati in silenzio.

### Web (da `web/`)

```sh
corepack pnpm run check-tokens                 # tokens.css deve coincidere con la styleguide
corepack pnpm install --frozen-lockfile
corepack pnpm run lint
corepack pnpm run typecheck
corepack pnpm run test
```

## Regole di scrittura

- **Contratto API:** non si modifica `api/openapi.yaml` (né i file generati) senza che l'item lo chieda; se serve, blocca e segnala.
- **Design system:** usa i token di `web/src/styles/tokens.css` e i componenti esistenti; `tokens.css` si rigenera con `corepack pnpm run sync-tokens`, non a mano.
- **Test web:** il typecheck non ha `@types/node`: i file del repo nei test si leggono con import `?raw`.
- **File rigenerati e fine riga:** su Windows dopo `generate-api.sh` o `go work sync` confronta con `git diff --stat --ignore-cr-at-eol` e committa solo ciò che cambia davvero; gli `.sh` restano LF e mode 100755.
- **Segreti:** nessun token, chiave o password in codice, log o messaggi di errore.

## Riferimenti

- `docs/agent-trial.md`: prova reale di un agente con `gs` e skills.
- `docs/operations.md`: backup, ripristino, prova di ripristino su macchina nuova e prova del mirror in push.
- `docs/rules-coverage.md`: regole di prodotto e test che le coprono.
- `.galaxylab/checks.toml`, `.github/workflows/ci.yml`: controlli di revisione e CI.
