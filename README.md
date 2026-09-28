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
services/   servizi Go (gateway, identity, git, core), un modulo per servizio
web/        web UI React + TypeScript
cli/        CLI gs in Go
skills/     pacchetto di skills per agenti di coding
deploy/     manifest k3s / Helm chart interno e installer
docs/       documentazione per chi installa e usa GitStack
```

Ogni cartella ha un proprio README con lo stato e i rimandi alle milestone che la riguardano.

## Licenze

- **AGPL-3.0** in radice (`LICENSE`): copre il prodotto server (`services/`, `web/`, `deploy/`, `docs/`). Scelta per proteggere dal "prendi e chiudi" — prendere il software e chiuderlo senza condividere i contributi — seguendo i precedenti di Forgejo, Grafana e Mattermost (decisione D13 [c_c0aa0b2a2659ff40]).
- **Apache-2.0** solo in `cli/` e `skills/` (un `LICENSE` dedicato in ciascuna cartella): il client non deve porre barriere all'integrazione negli strumenti degli agenti di coding, mentre il cuore del prodotto resta protetto da AGPL-3.0 (decisione D17 [c_1d1d61aca3dea601]).

GitStack è interamente open source; eventuali ricavi vengono da supporto e servizi, non dalla chiusura del codice (decisione D12 [c_85f1f52e96d98e7f]).

## Avviare lo sviluppo

Prerequisiti: Go (workspace in `go.work`, un modulo per servizio sotto `services/` più `cli/`) e Node.js con `pnpm` (vedi `web/package.json` → `packageManager`, e `web/pnpm-lock.yaml`).

```sh
# Go: tutti i moduli dei servizi e la CLI sono nel workspace di radice
go work sync
bash scripts/go-each.sh go build ./...   # dalla radice, un modulo alla volta

# Web UI
cd web
pnpm install
```

`go build ./...` non funziona dalla radice: con `go.work` la radice stessa non è un modulo, quindi il tool `go` va invocato dentro ciascun modulo. `scripts/go-each.sh` è l'unica fonte della lista dei moduli del workspace (`cli`, `services/core`, `services/gateway`, `services/git`, `services/identity`): lo usano sia questo README sia la pipeline CI (`.github/workflows/ci.yml`), così restano sempre allineati.

Questo è il primo item del monorepo (M-01/T-01): oggi contiene solo la struttura, le licenze e gli scheletri dei moduli (più un package `doc.go`/`main.go` minimo per modulo, così build/lint/test hanno qualcosa su cui lavorare). Il codice vero dei servizi, della web UI, della CLI e del deploy arriva con gli item successivi di M-01.

## CI

Ogni push e pull request fa girare `.github/workflows/ci.yml` (GitHub Actions, runner `ubuntu-latest`):

- **go**: `go work sync`, build, `golangci-lint` e `go test ./... -race` per ciascun modulo Go (`scripts/go-each.sh`).
- **ts**: `pnpm install`, lint, typecheck e test di `web/`.
- **registry**: build e push delle immagini dei servizi su un registry container (parametro `CONTAINER_REGISTRY`, default `ghcr.io`), come `ghcr.io/<owner>/gitstack-<servizio>` con tag sha del commit e, sui tag Git, anche il tag di versione. Solo su push a `main` o su tag, mai sulle PR; un servizio senza `Dockerfile` (arrivano con GIT-4/GIT-5) viene saltato senza far fallire la pipeline.

Gli stessi controlli Go e TS sono dichiarati in `.galaxylab/checks.toml` per l'esecuzione sui rami integrati.
