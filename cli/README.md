# cli/

`gs`, la CLI Go di GitStack per persone e agenti (stile `gh`), su client generato dall'OpenAPI del gateway. Dettagli: [[M-07]] in `.lmbrain-lite/milestones/M-07.md`.

Stato: fondamenta di M-07 (GIT-162): albero dei comandi, client sul gateway, configurazione multi-istanza, livello di output e codici di uscita. Comandi veri: `gs version`, `gs issue` (GIT-166), `gs repo` (GIT-165), `gs notification` e `gs api` (GIT-169), `gs search`, `gs browse` e `gs blame` (GIT-168); gli altri gruppi (`auth`, `repo`, ...) sono padri vuoti che gli item successivi riempiono.

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

Per aggiungere comandi a un gruppo si tocca solo `internal/cmd/<gruppo>/`: `root.go` registra già tutti i gruppi e non cambia più. `browse` e `blame` sono comandi foglia. I gruppi usano `cmdutil.GroupRun` come `RunE` (aiuto senza argomenti, exit 2 con un sottocomando sconosciuto) e `cmdutil.NoArgs` per i comandi senza argomenti, così l'uso errato esce sempre con 2.

`gs api <percorso>` (GIT-169) chiama l'API grezza; il vecchio `gs api user` provvisorio è sostituito (`gs api /auth/session` dà la stessa risposta).

## Configurazione e istanze (G7)

Cartella: `GS_CONFIG_DIR` oppure `<os.UserConfigDir()>/gs`.

```
config.yaml            default_host: l'ultima istanza configurata
hosts/<host>.yaml      user, git_protocol e, solo se non c'è il portachiavi, token
```

Il `:` di `host:porta` nel nome file diventa `_`. File `0600` e cartelle `0700` su Unix; su Windows la DACL del file (e delle cartelle) è protetta e ha un'unica voce, per l'utente corrente (`os.WriteFile` con `0600` su Windows non protegge niente). La scrittura è atomica (file temporaneo accanto, poi rename). I test lo verificano (`internal/config`: modo su Unix, lettura della DACL su Windows).

**Token.** L'interfaccia `config.TokenSource` ha due implementazioni: `KeyringTokens` (portachiavi di sistema via `github.com/zalando/go-keyring`, servizio `gs`, utente = host) e `FileTokens` (campo `token` di `hosts/<host>.yaml`, 0600). `KeyringTokens` usa il file come ripiego quando il portachiavi non c'è (nessun Secret Service, niente D-Bus) e legge anche un token già nel file. `GS_NO_KEYRING=1` disattiva il portachiavi. `GS_TOKEN` ha sempre la precedenza, ma vale solo per l'istanza scelta (`--hostname`, `GS_HOST`, o la predefinita). Il file host ha anche `host:`, il nome esatto dell'istanza (il nome del file perde i due punti).

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

## `gs issue` (GIT-166, G4)

Sottocomandi: `create`, `list` (`ls`), `view`, `edit`, `comment`, `close`, `reopen`, `lock`, `unlock`. Il repo è quello del remote `origin` o `-R owner/repo`. Chi li usa passa i numeri come `12` o `#12`. Le regole le applica l'API (I2, I3, I4, I11, R10): `gs` mostra l'errore e lo traduce nel codice di uscita.

| Comando | Cosa fa | Flag principali |
|---|---|---|
| `create` | apre una issue; su stdout l'indirizzo web | `-t/--title` (obbligatorio, salvo un modello che lo propone), `-b/--body`, `-F/--body-file` (`-` = stdin), `-l/--label`, `-a/--assignee` (`@me`), `-m/--milestone` (numero o titolo), `-T/--template` (I11) |
| `list` | elenca (default: aperte, `-L 30`) | `-s/--state open\|closed\|all`, `-l/--label` (tutte), `-a/--assignee` (utente, `@me`, `@agents`, `none`), `-A/--author` (utente, `@me`), `-m/--milestone` (numero, titolo, `none`), `-S/--search` (sintassi I10), `-L/--limit` |
| `view <n>` | titolo, stato, etichette, assegnatari, milestone, testo | `-c/--comments`, `-w/--web` |
| `edit <n>` | modifica | `-t`, `-b`, `-F`, `--add-label`, `--remove-label`, `--add-assignee`, `--remove-assignee`, `-m`, `--remove-milestone` |
| `comment <n>` | commenta; su stdout l'indirizzo del commento | `-b`, `-F` (`-` = stdin) |
| `close <n>` | chiude con un motivo (I2) | `-r/--reason completed\|"not planned"\|duplicate`, `-d/--duplicate-of <n>` (o `-r "duplicate #n"`), `-c/--comment` |
| `reopen <n>` | riapre e azzera il motivo | `-c/--comment` |
| `lock <n>` / `unlock <n>` | blocca o sblocca la discussione (I11, serve admin) | `lock -r/--reason` |

- **Filtri e ricerca.** `list` passa all'API (`GET /repos/{owner}/{repo}/issues`) i filtri e `--search` così come sono: la sintassi I10 la interpreta il server (`pkg/issuequery`), quindi `gs`, UI e API danno gli stessi risultati. `--state` si manda solo se lo scrivi: senza, vale `is:open|closed` di `--search`, e se manca anche quello l'API mostra le aperte. Le etichette dei filtri si sommano (la issue le ha tutte).
- **Modelli.** `create --template bug` legge `.gitstack/ISSUE_TEMPLATE/bug.md` dall'API (`GET .../issue-templates`): titolo, etichette (unite a quelle di `-l`) e testo precompilati, che `--title` e `--body` sovrascrivono. Se il modello non esiste, l'errore (exit 2) elenca quelli disponibili.
- **Milestone.** `-m` accetta il numero o il titolo (senza distinguere maiuscole); un titolo sconosciuto è «non trovato» (exit 6).
- **Repo archiviato (R10).** Ogni modifica risponde 409 `archived` e `gs` stampa «il repository owner/repo è archiviato (sola lettura): …» (exit 1). `list` e `view` funzionano.
- **Codici di uscita.** Quelli di G3: l'API risponde 401 → 4, 403 (anche `insufficient_scope`, `locked`) → 5, 404 → 6 (un repo che non puoi leggere è 404, non 403), 409 e 422 → 1; flag e argomenti sbagliati, `duplicate` senza numero, `edit` senza modifiche → 2. Una issue già chiusa o già aperta è 409 (exit 1), non un successo silenzioso.
- **I3.** L'autore chiude e riapre la propria issue anche con solo `read`; chi non ha `write` non gestisce quelle altrui (exit 5). Etichette, assegnatari e milestone chiedono `write`.

### Campi di `--json`

Sono quelli dello schema dell'API (camelCase), più `url` (pagina web). Un campo facoltativo assente è `null`. `--json` senza campi li elenca; un campo sconosciuto è un uso errato (exit 2).

| Comando | Oggetto | Campi |
|---|---|---|
| `create`, `edit`, `close`, `reopen`, `lock`, `unlock` | `Issue` | `id`, `number`, `title`, `body`, `state` (`open`\|`closed`), `closeReason` (`completed`\|`not_planned`\|`duplicate`), `duplicateOf`, `author`, `viaToken`, `labels`, `assignees`, `milestone`, `locked`, `hidden`, `edited`, `commentCount`, `attachments`, `closedAt`, `createdAt`, `updatedAt`, `url` |
| `view` | `Issue` | gli stessi, più `comments` (lista di commenti; si legge solo se richiesto con `--comments` o con `--json ...,comments`) |
| `list` | lista di `IssueSummary` | `number`, `title`, `state`, `closeReason`, `author`, `labels`, `assignees`, `milestone`, `locked`, `commentCount`, `createdAt`, `updatedAt`, `url` (senza `body`) |
| `comment` | `IssueComment` | `id`, `issueNumber`, `body`, `author`, `viaToken`, `edited`, `deleted`, `attachments`, `createdAt`, `updatedAt`, `url` |

`author` e ogni elemento di `assignees` sono `{id, username, kind: human|agent, displayName}`; `labels`: `{id, name, color}`; `milestone`: `{number, title, state}`. Senza `--json`, `list` stampa una riga per issue (`#n`, stato con motivo per le chiuse, etichette, data di aggiornamento, titolo), con intestazione solo su TTY.

### Test

`cli/internal/cmd/issue/issue_test.go`: gateway finto, richieste e corpi verificati. `services/core/internal/stackitest/gs_issue_integration_test.go` (`TestGsIssue`, tag `integration`): il binario `gs` vero compilato con `go build` contro lo stack completo (gateway, identity, git, core, Postgres), con un proxy che mappa `/api/v1` su `/v1`. Prova ogni sottocomando, i tre motivi di chiusura, I3, il blocco, R10, l'accordo di `list` con l'API (stessi numeri con filtri e `--search`) e i codici 4, 5 e 6.

## `gs repo` (GIT-165, G4)

Sottocomandi: `create`, `list` (`ls`), `view`, `clone`, `edit`, `archive`, `unarchive`, `delete` (`rm`), `restore`. Salvo `create`, `list` e `restore`, il repo è l'argomento `owner/repo` (anche un URL di clone), altrimenti `-R` o il remote `origin`. Le regole le applica l'API (R1, R2, R5, R9, R10, R12, P7): `gs` mostra l'errore e lo traduce nel codice di uscita.

| Comando | Cosa fa | Flag principali |
|---|---|---|
| `create [<owner>/]<nome>` | crea il repo; su stdout l'indirizzo web. Owner: l'utente che chiama, o l'organizzazione data nel nome o con `-o/--owner`. **Privato di default (P7)**; non esiste l'accesso anonimo (`--public` non c'è) | `-d/--description`, `--visibility private\|internal`, `--private`, `--internal`, `--add-readme`, `--gitignore <modello>`, `--license <modello>` (contenuto iniziale facoltativo, R5: primo commit su `main`), `--no-default-labels` |
| `list [<owner>]` | elenca i repo leggibili (`-L 30`); con `<owner>` solo i suoi | `--visibility`, `--archived`, `--no-archived`, `--deleted` (i repo eliminati ancora recuperabili), `-L/--limit` |
| `view [<repo>]` | nome, descrizione, visibilità, stato, branch principale, indirizzi di clone | `-w/--web` |
| `clone <repo> [<cartella>] [-- opzioni di git]` | `git clone` dell'indirizzo dell'installazione: HTTPS di default, SSH (`ssh://git@<host>:2222/...`, R7) con `-p/--protocol ssh` o se la configurazione dell'istanza ha `git_protocol: ssh`. Con HTTPS l'accesso usa il token via `gs auth setup-git` | `-p/--protocol https\|ssh` |
| `edit [<repo>]` | modifica (serve admin) | `-d`, `--visibility private\|internal`, `--default-branch`, `--protect-default-branch[=false]` (R9) |
| `archive [<repo>]` / `unarchive [<repo>]` | sola lettura (R10) e ritorno | `archive` chiede conferma (G8) |
| `delete [<repo>]` | elimina, recuperabile per 7 giorni (R2, R12) | conferma (G8) |
| `restore <repo>` | recupera un repo eliminato da meno di 7 giorni | |

- **Conferma G8.** `archive` e `delete` fanno riscrivere `owner/repo`. Con `--yes`/`-y` procedono senza chiedere. Senza terminale in ingresso e senza `--yes` escono con 2 senza leggere stdin e senza chiamare l'API; con terminale, un testo diverso annulla (exit 1). `unarchive` e `restore` non sono distruttivi e non chiedono.
- **Repo eliminati.** `gs repo list --deleted` mostra nome, data di eliminazione e di cancellazione definitiva; `restore` cerca tra quelli di `owner` e, se non c'è, esce 6.
- **Codici di uscita.** Quelli di G3: 401 → 4, 403 → 5, 404 → 6 (un repo che non puoi leggere è 404), 409 (nome già preso, repo già archiviato) e 422 → 1; flag e argomenti sbagliati, `edit` senza modifiche, SSH richiesto ma non offerto dall'istanza → 2.

### Campi di `--json` (repo)

Sono quelli dello schema `Repository` dell'API (camelCase), più `url` (pagina web). Un campo facoltativo assente è `null`. `--json` senza campi li elenca; un campo sconosciuto è un uso errato (exit 2). Valgono per `create`, `list`, `view`, `edit`, `archive`, `unarchive` e `restore` (`list` è una lista di oggetti).

| Campo | Contenuto |
|---|---|
| `id` | UUID della risorsa repo |
| `name`, `fullName` | nome e `owner/nome` |
| `owner` | `{name, type: user\|organization}` |
| `description` | testo, `""` se vuoto |
| `visibility` | `private` o `internal` |
| `archived`, `archivedAt` | R10 |
| `defaultBranch`, `empty`, `protectDefaultBranch` | branch principale, nessun commit, protezione R9 |
| `cloneUrls` | `{https, ssh, sshShort}`; `ssh` è assente se il server SSH è spento |
| `createdAt`, `updatedAt`, `url` | date e pagina web |

`gs repo list --deleted --json` elenca `DeletedRepository`: `id`, `name`, `owner`, `fullName`, `deletedAt`, `purgeAt` (dopo questo istante la cancellazione è definitiva) e `url`. Nelle due liste i campi dell'altra sono `null`.

### Test

`cli/internal/cmd/repo/repo_test.go`: gateway finto, richieste e corpi verificati, conferme G8 con e senza TTY. `services/core/internal/stackitest/gs_repo_integration_test.go` (`TestGsRepo`, tag `integration`): il binario `gs` vero contro lo stack completo, con `git` e `ssh` veri per `clone`. Prova create (privato di default, contenuto iniziale su `main`, owner organizzazione), list, view, clone HTTPS e SSH, edit, archive (push rifiutato, R10), delete, restore, la conferma G8 (senza TTY exit 2, `--yes`) e i codici 4, 5 e 6.

## `gs notification` e `gs api` (GIT-169, G4, C4)

**`gs notification`** è la casella dell'utente corrente, persona o agente (stessa API `/notifications`; ciclo C4: list, view, read).

- `list` (`ls`): le non lette; `--all` anche le lette, `--archived` le archiviate; `--reason assigned,mentioned` (motivi: assigned, mentioned, participating, subscribed, commit_linked, state_change, webhook); `-R owner/repo` filtra per repo (nessun ripiego sul remote origin); `-L` limite (30). `--json`/`--jq` con i campi di `Notification` più `url` (pagina web della issue collegata): `id, reason, read, archived, event, summary, repository, issue, commentId, webhook, actor, createdAt, readAt, url`.
- `read <id>` segna come letta (idempotente); `read --all` tutte le non lette, ristrette con `--reason` e `-R`. Con `--json` l'output di `--all` è `{"marked": n}`.
- `view <id>` mostra motivo, evento, repo, issue collegata e URL; non segna come letta. `--web` apre la issue nel browser. Una notifica altrui è 404 (exit 6).

**`gs api <percorso>`** è per quello che i comandi dedicati non coprono (organizzazioni, team, webhook, utenti, token agent). Il percorso è relativo a `/api/v1` e si risolve sull'istanza corrente (G7); `/orgs`, `orgs` e `/api/v1/orgs` sono la stessa chiamata, un indirizzo completo è un uso errato (exit 2).

| Flag | Effetto |
| --- | --- |
| `-X, --method` | metodo; default GET, POST se ci sono campi o corpo |
| `-f chiave=valore` | campo di testo |
| `-F chiave=valore` | campo tipizzato: `true`, `false`, `null`, interi sono JSON; `@file` (o `@-` da stdin) legge il valore da file |
| `--input file` | corpo da file o stdin (`-`), inviato così com'è; non si combina con -f/-F |
| `-H 'Nome: valore'` | intestazione aggiuntiva |
| `--paginate` | solo GET: segue `{items,page,perPage,total}` e stampa un solo array con tutti gli elementi |
| `--jq` | filtra la risposta (con `--paginate`, l'array unito) |
| `-i, --include` | stampa stato e intestazioni |

Sui GET i campi diventano parametri di query (la query nel percorso si mantiene), sugli altri metodi un corpo JSON; `a[b]=1` annida, `a[]=x` costruisce un array. Un errore dell'API stampa il corpo della risposta su stdout, il messaggio su stderr (JSON con `--jq`) ed esce col codice di G3 (4, 5, 6 o 1). Esempio G4: `gs api -X POST /users/botty/tokens -f name=ci -F 'scopes[]=read:resource' -f expiresAt=2026-12-31T00:00:00Z --jq .token`.

## `gs search`, `gs browse`, `gs blame` (GIT-168, G4)

Il repo è quello del remote `origin` o `-R owner/repo`; l'istanza e il token seguono G7. Le regole le applica l'API: `gs` mostra l'errore e lo traduce nel codice di uscita (un repo che non puoi leggere è 404 → 6).

| Comando | Cosa fa | Flag principali |
|---|---|---|
| `search issues <query>` | cerca issue su tutta l'installazione (`GET /search/issues`, I10), nei soli repo leggibili; tabella `REPO # STATO AGGIORNATA TITOLO` | `--sort created\|updated\|comments\|relevance`, `-L/--limit` (30) |
| `search code <testo>` | cerca un testo nel codice del repo (`GET /repos/{owner}/{repo}/search`, B5): `percorso:riga: frammento` | `--ref` (default: branch principale) |
| `blame <file>` | per ogni riga: commit, autore (con badge `[agent]` se l'email è di un utente agent), data, numero e testo | `--ref` |
| `blame --history <file>` | **storico del file** (B4): i commit che lo toccano, dal più recente; `COMMIT DATA AUTORE OGGETTO` | `--ref`, `--author` (nome o email), `-L/--limit` (30) |
| `browse [percorso[:riga] \| numero]` | apre la UI: repo, file (`#L<riga>`), cartella (`percorso/`), issue (`12` o `#12`) | `-b/--branch`, `--history`, `--blame`, `--issues`, `-n/--no-browser` |

- **Sintassi di `search issues`.** È quella di I10 (`is:`, `reason:`, `label:`, `assignee:` con `@me` e `@agents`, `author:`, `milestone:`, `no:`, `repo:`, `org:`, testo libero), interpretata dal server come per `gs issue list --search`; più parole sono una sola query. Se i risultati sono più di quelli mostrati (`-L`), `gs` lo dice su stderr («Mostrate N issue su M»). Una query non interpretabile è 422 (exit 1).
- **Limite di 100 in `search code` (B5).** Il server restituisce al massimo 100 risultati e interrompe dopo 10 secondi. Quando succede `gs` lo scrive su stderr («Risultati limitati ai primi 100: ce ne sono altri…», «Ricerca interrotta dopo 10 secondi…») e in `--json` valgono `limitReached` / `timedOut`: l'elenco su stdout resta pulito. Il testo da cercare ha da 2 a 256 caratteri (altrimenti exit 2, senza rete).
- **Blame e storico (B4).** Un file binario o oltre 1 MB non ha il blame: l'API risponde 400 `blame_unavailable` e `gs` esce 1 con «blame non disponibile per <file>: il file è binario o supera 1 MB (B4)». Il testo delle righe si legge dal file allo stesso ref (non serve con `--json`). Lo storico nel terminale è `gs blame --history <file>`; nel browser `gs browse --history <file>` (pagina commits del file). `--author` e `-L` valgono solo con `--history`.
- **`browse`.** Senza `-b` il ref è il branch principale del repo (una chiamata a `GET /repos/{owner}/{repo}`; con `-b` o per issue e `--issues` niente rete). Gli indirizzi sono quelli della UI (`web/src/App.tsx`): `/o/r`, `/o/r/tree/<ref>[/<dir>]`, `/o/r/blob/<ref>/<file>#L<n>`, `/o/r/blame/<ref>/<file>`, `/o/r/commits/<ref>[/<file>]`, `/o/r/issues[/<n>]`; i segmenti sono codificati. Con `--no-browser` l'indirizzo va su stdout e il browser non si apre; altrimenti `gs` scrive «Apro <url> nel browser» su stderr. `--history`, `--blame` e `--issues` sono alternativi (exit 2).

### Campi di `--json`

| Comando | Oggetto | Campi |
|---|---|---|
| `search issues` | lista | `repo` (`owner/nome`), `number`, `title`, `state`, `closeReason`, `author`, `labels`, `assignees`, `milestone`, `locked`, `commentCount`, `createdAt`, `updatedAt`, `url` |
| `search code` | oggetto | `query`, `ref`, `limitReached`, `timedOut`, `results` (lista di `{path, line, fragment, url}`) |
| `blame` | oggetto | `path`, `ref`, `ranges` (`{startLine, endLine, commit}`; l'autore del commit ha `user.kind` `human`\|`agent` se riconosciuto), `url` |
| `blame --history` | lista di `CommitSummary` | `sha`, `subject`, `message`, `author`, `committer`, `parents`, `url` |

`blame --json` accetta l'unione dei campi di blame e storico; quelli dell'altra modalità non compaiono.

### Test

`cli/internal/cmd/{search,blame,browse}/*_test.go`: gateway finto; `browse` prova `ParseArg` e `URL` (unitari) e che `--no-browser` stampi l'indirizzo. `services/core/internal/stackitest/gs_code_integration_test.go` (`TestGsCode`, tag `integration`): `gs` vero sullo stack completo con la storia di `TestBrowserCodice` (agente botty, file oltre 1 MB, binari): ricerca issue su tutta l'installazione con visibilità (carol non vede il privato), ricerca nel codice con il limite di 100, blame con badge agent, errori B4, storico con `--author` e `--ref`, URL di `browse`.

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

## gs auth (GIT-164)

| Comando | Cosa fa |
|---|---|
| `gs auth login [--hostname H] [--with-token] [--web]` | Chiede istanza e token (input nascosto), verifica il token con `GET /auth/session` e lo salva (portachiavi, altrimenti file 0600). `--with-token` legge il token da stdin. `--web` apre `https://<host>/settings/tokens/new?name=gs&scopes=read:user,read:org,read:resource,write:resource&expires=90` (percorso fissato in `web/README.md`) e poi chiede il token incollato. Un token rifiutato esce con 4 e non salva niente |
| `gs auth status [--json campi]` | Per ogni istanza configurata (o `--hostname`): utente, sorgente del token (`keyring`, `file`, `GS_TOKEN`), scope, scadenza, stato (`ok`, `invalid`, `no_token`, `error`). Exit 4 se un token è scaduto, revocato o mancante; 1 se un'istanza non risponde |
| `gs auth logout [--hostname H]` | Toglie token e configurazione dell'istanza (non revoca il token sul server) |
| `gs auth setup-git [--hostname H]` | Scrive in `git config --global` `credential.<schema>://<host>.helper` = `!'<gs>' auth git-credential` per ogni istanza configurata |
| `gs auth git-credential get` | Nascosto: il helper chiamato da git. Risponde `username` e `password` (il token) solo per l'istanza configurata con quello schema e host |

Agenti e CI: `GS_HOST` e `GS_TOKEN` bastano, senza login né file; con `gs auth setup-git` anche git in HTTPS usa il token. Il token non va mai su stdout/stderr né nei log. Il controllo di versione contro `/meta` (G6, `internal/compat`) è collegato alla radice (`PersistentPreRunE`) e salta `version`, `help`, `completion` e `auth login|logout|setup-git|git-credential`.

Test: `internal/cmd/auth` con portachiavi finto (`keyring.MockInit`, `MockInitWithError` per il ripiego su file); `setupgit_integration_test.go` (tag `integration`, serve `git`) compila il binario gs e clona in HTTPS con git vero da un `git http-backend` che pretende il token in Basic auth.
