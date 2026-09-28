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

Prerequisiti: Go (workspace in `go.work`, un modulo per servizio sotto `services/` più `cli/`) e Node.js con `pnpm` (vedi `web/package.json` → `packageManager`, e `web/pnpm-lock.yaml`).

```sh
# Go: tutti i moduli dei servizi, la CLI e il client Go sono nel workspace di radice
go build ./...          # dalla radice, una volta che i servizi hanno codice
go work sync

# Web UI
cd web
pnpm install
```

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

In CI il check `api-contract` (`.github/workflows/api-contract.yml`) lancia
lo stesso script e fallisce se il codice generato committato non è allineato
al contratto. Dettagli: `api/README.md`, `client/README.md`.

Stato (M-01): la struttura del monorepo, le licenze e il contratto API sono a posto (T-01, T-03); il codice dei servizi, della web UI, della CLI e del deploy arriva con gli item successivi di M-01.
