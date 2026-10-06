# cli/

`gs`, la CLI Go di GitStack per persone e agenti (stile `gh`), su client generato dall'OpenAPI del gateway. Dettagli: [[M-07]] in `.lmbrain-lite/milestones/M-07.md`.

Stato: fondamenta di M-07 (GIT-162): albero dei comandi, client sul gateway, configurazione multi-istanza, livello di output e codici di uscita. Comandi veri: `gs version` e il provvisorio `gs api user`; gli altri gruppi (`auth`, `repo`, `issue`, ...) sono padri vuoti che gli item successivi riempiono.

## Licenza

Apache-2.0, non AGPL-3.0 (vedi `LICENSE` in questa cartella). Decisione D17 [c_1d1d61aca3dea601]: il client (CLI e skills) usa una licenza permissiva per non porre barriere all'integrazione negli strumenti degli agenti di coding; il cuore del prodotto (i servizi in `services/`) resta protetto da AGPL-3.0.

## Librerie scelte

Decise dal CTO (GIT-162), tutte con licenza compatibile con Apache-2.0 (D17). Gli item successivi di M-07 le danno per fatte.

| Uso | Libreria | Versione | Licenza |
|---|---|---|---|
| Albero dei comandi | `github.com/spf13/cobra` (+ `spf13/pflag`) | v1.10.2 | Apache-2.0 (pflag BSD-3-Clause) |
| `--jq` | `github.com/itchyny/gojq` | v0.12.19 | MIT |
| Portachiavi (lo integra GIT-164, non è ancora usato) | `github.com/zalando/go-keyring` (+ `godbus/dbus` BSD-2-Clause, `danieljoos/wincred` MIT) | v0.2.8 | MIT |
| Input nascosto, rilevamento TTY | `golang.org/x/term` | | BSD-3-Clause |
| ACL su Windows | `golang.org/x/sys/windows` | | BSD-3-Clause |
| File YAML di configurazione | `gopkg.in/yaml.v3` | v3.0.1 | MIT / Apache-2.0 |
| Client dell'API | `github.com/fathorMB/GitStack/client/go` (generato con oapi-codegen) | pseudo-versione | Apache-2.0 |

`cli/go.mod` richiede `client/go` con una pseudo-versione (data UTC e hash di un commit di main); nel workspace vale la copia locale. Se `client/go` cambia in modo che serve alla CLI, si aggiorna la pseudo-versione e si lancia `go work sync`.

## Struttura

```
cmd/gs/main.go            var version = "dev" (-ldflags "-X main.version=<tag>"), chiama root.Run
internal/cmd/root         comando radice, flag globali, registra TUTTI i gruppi, Run (exit code, errori)
internal/cmd/<gruppo>     un pacchetto per gruppo, con NewCmd(f *cmdutil.Factory) *cobra.Command:
                          auth repo issue label milestone search browse blame notification api skills version
internal/cmdutil          Factory, IOStreams, errori e codici di uscita, ConfirmOrYes (G8)
internal/config           configurazione multi-istanza, TokenSource, permessi solo-utente
internal/gsrepo           istanza e owner/repo dal remote origin
internal/output           tabelle, --json, --jq, errore JSON
internal/api              client sul gateway (sopra client/go) e conversione degli errori
```

Per aggiungere comandi a un gruppo si tocca solo `internal/cmd/<gruppo>/`: `root.go` registra già tutti i gruppi e non cambia più. `browse` e `blame` sono comandi foglia e finché non sono implementati escono con 2 («non ancora implementato»). I gruppi usano `cmdutil.GroupRun` come `RunE` (aiuto senza argomenti, exit 2 con un sottocomando sconosciuto) e `cmdutil.NoArgs` per i comandi senza argomenti, così l'uso errato esce sempre con 2.

`gs api user` (GET `/auth/session`) è provvisorio: esercita configurazione, client, output ed exit code; GIT-169 lo sostituisce con `gs api <endpoint>`.

## Configurazione e istanze (G7)

Cartella: `GS_CONFIG_DIR` oppure `<os.UserConfigDir()>/gs`.

```
config.yaml            default_host: l'ultima istanza configurata
hosts/<host>.yaml      user, git_protocol e, solo se non c'è il portachiavi, token
```

Il `:` di `host:porta` nel nome file diventa `_`. File `0600` e cartelle `0700` su Unix; su Windows la DACL del file (e delle cartelle) è protetta e ha un'unica voce, per l'utente corrente (`os.WriteFile` con `0600` su Windows non protegge niente). La scrittura è atomica (file temporaneo accanto, poi rename). I test lo verificano (`internal/config`: modo su Unix, lettura della DACL su Windows).

**Token.** L'interfaccia `config.TokenSource` ha oggi l'implementazione su file; `GS_TOKEN` ha sempre la precedenza. GIT-164 vi aggiunge il portachiavi dietro la stessa interfaccia.

**Scelta dell'istanza** (`Factory.Host`), nell'ordine:

1. `--hostname <host[:porta]>`;
2. l'host del remote `origin` se si è in un repo;
3. `GS_HOST`;
4. l'istanza predefinita (`default_host`).

Nessuna: exit 4 («nessuna istanza»). Senza token per l'istanza: exit 4.

**Scelta del repo** (`Factory.BaseRepo`): `--repo owner/repo` (`-R`), altrimenti il remote `origin`; altrimenti uso errato (exit 2).

Remote riconosciuti (`gsrepo.ParseRemote`):

| Remote | Host | Repo |
|---|---|---|
| `https://host[:porta]/owner/repo[.git]` | `host[:porta]` | `owner/repo` |
| `git@host:owner/repo.git` | `host` | `owner/repo` |
| `ssh://git@host:2222/owner/repo.git` | `host` (la porta SSH non fa parte dell'istanza) | `owner/repo` |

Fuori da un repo, o con un remote non riconosciuto, il remote si ignora.

## Client

`internal/api` costruisce, sopra `client/go`, un client con base `https://<host>/api/v1` (un host con schema esplicito, per esempio `http://127.0.0.1:8080`, lo mantiene), `User-Agent: gs/<versione>` e `Authorization: Bearer <token>`. Le risposte non-2xx diventano `*cmdutil.APIError` (stato, `error.code`, `error.message` del formato errore unico). `Client.Generated()` dà il client generato; con `api.CheckStatus(resp.StatusCode(), resp.Body)` si convertono le sue risposte. `Client.Do(ctx, method, path, body, header)` è la chiamata grezza per le rotte non coperte.

## Contratto di output (G3)

**Tabelle.** Su TTY: intestazione, colonne allineate, ultima colonna troncata (`…`) alla larghezza del terminale. Senza TTY: nessuna intestazione, una riga per record, colonne separate da tab, mai troncate.

**`--json campo1,campo2`.** Stampa su stdout i soli campi richiesti (una lista di oggetti per i comandi di elenco), compatto senza TTY e indentato su TTY. Un campo sconosciuto è un uso errato (exit 2) e l'errore elenca quelli disponibili. **`--json` senza campi** elenca i campi disponibili su stdout ed esce 0, come `gh`. I comandi lo aggiungono con `output.AddJSONFlags(cmd, &opts, campi)` e stampano con `opts.Write(...)`.

**`--jq <espressione>`** (`-q`, gojq) filtra il JSON; da solo vale su tutti i campi. Le stringhe si stampano senza apici, il resto come JSON. Un'espressione non valida è un uso errato (exit 2), un errore a runtime exit 1.

**Errori.** Senza JSON: `gs: <messaggio>` su stderr. Con `--json` o `--jq`: una riga su stderr, nella stessa forma dell'API, e stdout vuoto:

```json
{"error":{"code":"insufficient_scope","message":"..."}}
```

`code` è quello dell'API quando c'è, altrimenti uno di gs: `usage_error`, `not_authenticated` (manca istanza o token), `unauthenticated`, `forbidden`, `not_found`, `error`.

## Codici di uscita (G3)

| Codice | Significato | Origine |
|---|---|---|
| 0 | ok | anche `--json` senza campi e `--help` |
| 1 | errore generico | tutto il resto: 400, 409, 422, 5xx, rete, annullato |
| 2 | uso errato | flag, argomenti, comando sconosciuto, conferma G8 senza TTY e senza `--yes` |
| 4 | non autenticato | HTTP 401; nessun token o nessuna istanza in locale |
| 5 | permesso negato | HTTP 403, anche con `insufficient_scope` |
| 6 | non trovato | HTTP 404 |

La mappatura è in `cmdutil.ExitCode`, dagli errori tipizzati (`APIError`, `UsageError`, `ExitError`).

## Conferma delle operazioni distruttive (G8)

`cmdutil.ConfirmOrYes(io, yes, prompt, expected)`: con `--yes` procede; senza TTY in ingresso e senza `--yes` è un uso errato (exit 2) e non legge stdin; con TTY scrive il prompt su stderr e, se `expected` non è vuoto (per esempio `owner/repo`), vuole che venga riscritto identico, altrimenti accetta `y`/`yes`; una risposta diversa è `ErrCancelled` (exit 1).

## Versione e build

```sh
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=<tag>" -o gs ./cmd/gs
gs version      # gs version <tag>
gs --version
```

Senza `-ldflags` la versione è `dev`. `cmd/gs/main_test.go` verifica l'iniezione e compila senza cgo per linux, darwin e windows × amd64, arm64 (i test saltano con `go test -short`).

## Distribuzione (GIT-171, G6)

I binari si costruiscono con `scripts/build-gs-dist.sh <cartella> <versione>`: `gs_<os>_<arch>` per linux, darwin e windows su amd64 e arm64 (`.exe` su windows), `SHA256SUMS`, `gs-skills.zip` (cartella `skills/`) e `index.json`. La versione (`-X main.version=`) è `sha-<commit>`, come il tag delle immagini. Lo stesso script gira nel job CI `gs-binaries` (asset della release `sha-<commit>`, solo dopo il merge) e nello stage `gs` di `web/Dockerfile`: ogni istanza serve i file su `/downloads` e gli script `/install-gs.sh` e `/install-gs.ps1` (sorgenti in `web/deploy/`), senza internet. Dettagli, rotte e gestione della CA interna: `deploy/gitstack/README.md`, sezione «Download di gs e skills».

```sh
curl -fsSL https://<host>/install-gs.sh | sh        # Linux, macOS
irm https://<host>/install-gs.ps1 | iex             # Windows
```
