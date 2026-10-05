# GitStack

GitStack è un "mini GitHub" open source da installare on-prem con poca configurazione: repository Git remoti e Issues, gestibili da web UI, CLI o agenti. A lungo termine cresce fino a un piccolo cloud privato ("un piccolo Azure") dove si fanno build, deploy e hosting di applicazioni in container. Visione completa, architettura e decisioni in `.lmbrain-lite/knowledge/` (`vision.md`, `architecture.md`, `decisions.md`).

## Stack

- **Backend**: Go, pochi servizi "grossi" con confini netti — `gateway` (ingresso API), `identity` (utenti, token, OIDC), `git` (push/pull, hook), `core` (repo, issues) — dietro Traefik su k3s.
- **Dati**: PostgreSQL (uno schema per servizio) e repo Git su volume persistente.
- **Eventi**: NATS con JetStream, bus tra servizi e base per le milestone future (CI, deploy).
- **Web UI**: React + TypeScript (SPA), parla solo con l'API pubblica del gateway — stessa API di CLI, skills e (in futuro) server MCP.
- **CLI e agenti**: `gs` in Go (stile `gh`) + pacchetto di skills, su client generato dall'OpenAPI.
- **Piattaforma**: k3s incluso nell'installer, un comando per installare su una macchina Linux (Windows via WSL2).

Dettagli dei componenti e dei flussi chiave: `.lmbrain-lite/knowledge/architecture.md`. Tutte le decisioni di prodotto e architettura, con le motivazioni: `.lmbrain-lite/knowledge/decisions.md`.

## Struttura del monorepo

```
api/        contratto OpenAPI unico dell'API pubblica (fonte di verità)
services/   servizi Go (gateway, identity, git, core), un modulo per servizio
web/        web UI React + TypeScript
cli/        CLI gs in Go
client/     client Go e TypeScript generati dal contratto OpenAPI
skills/     pacchetto di skills per agenti di coding
deploy/     manifest k3s / Helm chart interno e installer
docs/       documentazione per chi installa e usa GitStack
```

Ogni cartella ha un proprio README con lo stato e i rimandi alle milestone che la riguardano.

## Licenze

- **AGPL-3.0** in radice (`LICENSE`): copre il prodotto server (`services/`, `web/`, `deploy/`, `docs/`). Scelta per proteggere dal "prendi e chiudi" — prendere il software e chiuderlo senza condividere i contributi — seguendo i precedenti di Forgejo, Grafana e Mattermost (decisione D13 [c_c0aa0b2a2659ff40]).
- **Apache-2.0** solo in `cli/`, `client/` (`client/go/`, `client/ts/`) e `skills/` (un `LICENSE` dedicato in ciascuna cartella): il client non deve porre barriere all'integrazione negli strumenti degli agenti di coding, mentre il cuore del prodotto — incluso il contratto OpenAPI in `api/` — resta protetto da AGPL-3.0 (decisione D17 [c_1d1d61aca3dea601]).

GitStack è interamente open source; eventuali ricavi vengono da supporto e servizi, non dalla chiusura del codice (decisione D12 [c_85f1f52e96d98e7f]).

## Avviare lo sviluppo

Prerequisiti: Go (workspace in `go.work`, un modulo per servizio sotto `services/` più `cli/` e `client/go/`) e Node.js con `pnpm` (vedi `web/package.json` → `packageManager`, e `web/pnpm-lock.yaml`).

```sh
# Go: tutti i moduli dei servizi, la CLI e il client Go sono nel workspace di radice
go work sync
bash scripts/go-each.sh go build ./...   # dalla radice, un modulo alla volta

# Web UI
cd web
pnpm install
```

`go build ./...` non funziona dalla radice: con `go.work` la radice stessa non è un modulo, quindi il tool `go` va invocato dentro ciascun modulo. `scripts/go-each.sh` risolve il problema ricavando la lista dei moduli da `go list -m` (cioè da `go.work` stesso, non da una lista scritta a mano) e la usa per iterare: lo usano sia questo README sia la pipeline CI (`.github/workflows/ci.yml`), così restano sempre allineati a `go.work` anche quando si aggiunge o toglie un modulo.

## Ambiente di sviluppo locale (cluster k3d)

Per provare le modifiche su un cluster reale, senza una VM: `make dev-up` crea un cluster [k3d](https://k3d.io/) locale e installa GitStack (chart `deploy/gitstack`, GIT-8) dalle immagini costruite in locale; `make dev-down` lo distrugge; `make dev-redeploy SVC=gateway` (o `core`/`web`) ricostruisce e ridistribuisce un solo servizio. Guida completa (prerequisiti Linux/macOS/Windows-WSL2, ciclo modifica → rebuild → redeploy): `docs/dev-environment.md`.

## Contratto API e client generati

`api/openapi.yaml` è la fonte unica dell'API pubblica (decisione D8
[c_f68d7615da93197a]); da lì si generano il client Go (`client/go/`) e il
client TypeScript (`client/ts/`) — codice generato, non si modifica a mano.
Un comando, dalla radice del monorepo, dopo `go work sync`, rigenera
entrambi:

```sh
go work sync
./scripts/generate-api.sh
```

In CI il workflow `api-contract` (`.github/workflows/api-contract.yml`) ha
due job separati (GIT-20), apposta non accoppiati:

- `check-generated` lancia `./scripts/check-api-generated.sh` e fallisce se
  il codice generato committato (`client/go/*.gen.go`,
  `client/ts/src/generated/**`, `internal/openapi/api.gen.go` di gateway e
  core) non è allineato al contratto.
- `workspace-sync` lancia `./scripts/check-go-work-sync.sh`, che esegue
  `go work sync` e fallisce se sporca go.mod/go.sum/go.work.sum di un
  modulo qualsiasi del workspace: `go work sync` alza le dipendenze
  indirette condivise in **tutti** i moduli, anche quelli che una modifica
  non ha toccato, quindi questo controllo deve restare separato da quello
  del contratto API (altrimenti un modulo nuovo o un cambio di dipendenza
  fa fallire "codice generato non allineato" per moduli estranei al
  contratto, un falso indizio già capitato con GIT-16 e GIT-19).

Dopo aver aggiunto un modulo al workspace o cambiato una dipendenza, lancia
`go work sync` dalla radice e committa tutti i go.mod/go.sum che cambiano,
non solo quelli del modulo toccato. Dettagli: `api/README.md`,
`client/README.md`.

## CI

`.github/workflows/ci.yml` (GitHub Actions, runner `ubuntu-latest`) gira su
ogni pull request sempre, e sui push solo verso `main` e sui tag (non su
ogni push di ramo, per non far girare la pipeline due volte sulle PR dello
stesso repo):

- **go**: `go work sync`, build, `golangci-lint` e `go test ./... -race` per ciascun modulo Go, incluso `client/go` (`scripts/go-each.sh`).
- **ts**: `pnpm install`, lint, typecheck e test di `web/`.
- **registry**: build e push delle immagini dei servizi su un registry container (parametro `CONTAINER_REGISTRY`, default `ghcr.io`), come `ghcr.io/<owner>/gitstack-<servizio>` con tag sha del commit e, sui tag Git, anche il tag di versione. Gira solo dopo che `go` e `ts` sono verdi, solo su push a `main` o su tag, mai sulle PR; un servizio senza `Dockerfile` (arrivano con GIT-4/GIT-5) viene saltato senza far fallire la pipeline.

Riepilogo di quando gira ogni workflow e perché:

| Workflow | Quando gira | Perché |
| --- | --- | --- |
| `ci.yml` | ogni pull request; push su `main` e sui tag `v*` | build, lint e test a ogni cambio che entra; pubblicazione delle immagini solo da `main` e dai tag |
| `api-contract.yml` | push su `main` e ogni pull request, senza filtro `paths:` | il contratto e i client generati non devono derivare, anche per cambi a soli `go.work`/`go.mod`/`go.sum` |
| `security.yml` | ogni pull request; ogni notte alle 03:17 UTC su `main` (schedule); a mano (`workflow_dispatch`, con `release` per il dry-run); sui tag `v*`, richiamato da `ci.yml` in modalità release, bloccante | le scansioni sono lente e lanciano circa 15 job: a ogni push su `main` consumavano troppi minuti e job paralleli del piano gratuito (decisione del board del 2026-10-05), quindi su `main` girano di notte, in segnalazione |

Il contratto API ha il suo workflow dedicato, `.github/workflows/api-contract.yml` (vedi sopra, sezione "Contratto API e client generati"), che gira sia su push a `main` sia su pull request, su qualunque file cambi (nessun filtro `paths:`), così anche un cambio ai soli `go.work`/`go.mod`/`go.sum` fa girare `workspace-sync`.

Stato (M-01): la struttura del monorepo, le licenze, il contratto API e la pipeline CI sono a posto (T-01, T-02, T-03); il codice vero dei servizi, della web UI, della CLI e del deploy arriva con gli item successivi di M-01. I moduli Go hanno solo un package `doc.go`/`main.go` minimo, così build/lint/test hanno qualcosa su cui lavorare.

Oltre alla CI su GitHub Actions, il motore GalaxyLab legge `.galaxylab/checks.toml` (solo dal ramo `main`) ed esegue i controlli lì dichiarati a ogni `integrate`. Per ora c'è un solo controllo, `go-build`, con i pattern espliciti dei moduli del workspace (niente bash/pnpm/golangci-lint: prerequisiti e motivazioni sono commentati in testa al file).

## Scansioni di sicurezza

`.github/workflows/security.yml` (GIT-114, regola V2 di rilascio v1.0) scansiona dipendenze (`govulncheck` su tutti i moduli di `go.work`, `pnpm audit` su web), codice (`gosec` per Go, `eslint-plugin-security` per TypeScript) e le cinque immagini (Trivy, stesso sha del job `registry`). Segnala (annotazioni e riepilogo nel run) senza bloccare sulle pull request, ogni notte alle 03:17 UTC su `main` (schedule, non più a ogni push, per il consumo di minuti e di job paralleli) e a mano; il run notturno scansiona l'ultimo `main` (il job delle immagini costruisce dal checkout); sui tag `v*`, comprese le `-rc.N`, un CRITICAL o HIGH senza eccezione fa fallire il job e il job `registry` non pubblica. Le eccezioni stanno in `.github/security-exceptions.json` (motivo e scadenza obbligatori). Scelte (gosec/ESLint invece di CodeQL), uso, dry-run e risultati del primo giro: [`docs/security-scans.md`](docs/security-scans.md).
