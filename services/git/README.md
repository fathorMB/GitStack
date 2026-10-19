# git

Servizio git di GitStack: tiene i repo bare su un volume e li gestisce con il binario `git` ufficiale (`os/exec`). In M-03/D espone solo l'API interna di ciclo di vita dei repo, chiamata da **core**; push/pull HTTPS e SSH, letture e hook `git.push` arrivano con gli item successivi di M-03. Il deploy nel chart è M-03/L.

## Configurazione (variabili d'ambiente)

| Variabile | Default | Note |
|---|---|---|
| `GITSTACK_GIT_DATA_DIR` | — (obbligatoria) | Directory dei repo; in produzione il PVC `git-data`. Nell'immagine vale `/data`. |
| `GITSTACK_GIT_ADDR` | `:8080` | Indirizzo di ascolto HTTP. |
| `GITSTACK_GIT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. Log JSON (slog) su stdout. |
| `GITSTACK_GIT_SSH_ADDR` | `:2222` | Indirizzo del server SSH integrato (R7); `off` lo disattiva. Il chart lo imposta da `git.ssh.port` (o `off` se `git.ssh.enabled=false`). |
| `GITSTACK_GIT_SSH_HOST_KEY_FILE` | `<DATA_DIR>/ssh/ssh_host_ed25519_key` | File PEM della chiave host ed25519. Se non esiste viene generata al primo avvio (0600) e riusata dai riavvii. Nel chart è il Secret `<release>-git-ssh-host-key` montato in `/etc/gitstack/ssh`, generato una sola volta. |
| `GITSTACK_IDENTITY_URL`, `GITSTACK_CORE_URL` | — | Servizi interrogati da smart HTTP e SSH (token, chiavi, permessi, owner/repo). Senza, o senza segreto di servizio, né smart HTTP né SSH partono (avviso nel log). |
| `GITSTACK_GIT_NATS_URL` | — | Bus NATS JetStream (es. `nats://nats:4222`) su cui pubblicare `git.push` dopo ogni push accettato, HTTPS o SSH (schema in [`docs/events.md`](../../docs/events.md)). Vuoto = nessun evento (avviso nel log). NATS irraggiungibile non ferma né l'avvio né i push: la pubblicazione si ritenta in background e, se non riesce, l'errore va nel log. Il chart la imposta all'URL del NATS interno. |
| `GITSTACK_SERVICE_SECRET_GIT` | — | Segreto proprio del servizio git (Secret `<release>-service-secrets`, chiave `git`, F9): Bearer verso identity e firma dell'identità verso core. Può essere un elenco `nuova,vecchia` (rotazione, `docs/operations.md`): la prima firma, tutte verificano. Non finisce mai nei log. |
| `GITSTACK_SERVICE_SECRET_CORE` | — | Segreto di core: con questo git verifica `/internal/*`. Senza, ogni chiamata a `/internal/*` è 401. Elenco `nuova,vecchia` accettato. Git non riceve la chiave del gateway. |
| `GITSTACK_IDENTITY_SERVICE_SECRET` | — | Segreto condiviso storico: vale solo per i due segreti sopra che mancano (installazioni fuori dal chart). |
| `GITSTACK_GIT_MAX_FILE_SIZE` | `100MB` | Dimensione massima di un file (blob) in un push (R6). Numero di byte o con suffisso `KB`, `MB`, `GB` (multipli di 1024: 100MB = 104 857 600 byte); `0` = nessun limite. |
| `GITSTACK_GIT_REPO_SIZE_WARN` | `5GB` | Oltre questa dimensione del repo il push è accettato con un avviso (R6, avviso di quota per repo). Stesso formato; `0` = nessun avviso. |
| `GITSTACK_GIT_MIRROR_MAX_FETCH_SIZE` | `5GB` | Tetto di crescita del repo in un singolo fetch di un pull mirror (F18): oltre, il fetch è interrotto, gli oggetti scaricati sono eliminati e l'aggiornamento fallisce. Stesso formato; `0` = nessun tetto. |
| `GITSTACK_GIT_RECEIVE_FSCK` | `on` | `off` spegne `receive.fsckObjects` sul receive-pack (F18). Con il controllo attivo git rifiuta il push di un oggetto malformato; zero-padded filemode e fuso orario non valido (`badTimezone`), tipici delle storie importate, restano avvisi (`receive.fsck.zeroPaddedFilemode=warn`, `receive.fsck.badTimezone=warn`). Se un import ha altre anomalie (per esempio `missingEmail`), `off` fa passare il push: riattivalo dopo l'import. |

Probe: `GET /healthz` (liveness, sempre `ok`) e `GET /readyz` (503 se `DATA_DIR` non esiste o non è scrivibile).

## Server SSH (M-03/I, R7)

Porta **2222** di default (`GITSTACK_GIT_SSH_ADDR`, nel chart `git.ssh.port`); l'installer non tocca mai l'SSH dell'host. Indirizzo: `ssh://git@<host>:2222/<owner>/<repo>.git`; la forma corta `git@<host>:<owner>/<repo>.git` vale solo con porta 22.

- Solo chiave pubblica, login `git`. Il fingerprint `SHA256:...` della chiave si risolve con identity `GET /internal/ssh-keys/{fingerprint}` (percorso con `url.PathEscape`); chiave sconosciuta o utente disattivato: rifiuto.
- Nessuna shell, pty, subsystem o forwarding. Si accettano solo `git-upload-pack` e `git-receive-pack` (anche `git upload-pack`/`git receive-pack`) su `[/]<owner>/<repo>[.git]`, senza argomenti extra, senza metacaratteri; ogni altro comando esce con errore 128.
- Permessi: core `GET /repos/{owner}/{repo}` con l'identità firmata dell'utente (404 per «non esiste» e «non puoi leggerlo», stesso messaggio) e identity `POST /internal/permissions/check`: `read` per upload-pack, `write` per receive-pack; un repo archiviato rifiuta il push. È lo stesso `access.Authorizer` dello smart HTTP (`write=true` per receive-pack), con un solo `upstream.Client`; una chiave SSH vale come l'utente intero (scope read e write), restano ruolo e archiviazione. Il repo archiviato (R10) si legge ma rifiuta il push, anche via HTTPS (403) e via SSH (`access.ErrArchived`). La chiave si risolve con `upstream.Client.LookupKey`.
- Chiave host: vedi la variabile sopra; non cambia a ogni riavvio, quindi i client non vedono «host key changed».
- `internal/sshd`; i test usano un client `ssh`/`git` reale (`t.Skip` se mancano dal PATH).

## Regole alla ricezione del push (M-03/J: R6, R9, R10)

Valgono uguali per HTTPS e SSH, lato server, prima che il push entri nel repo. R6 e R9 stanno in un hook **pre-receive** unico (`internal/receiverules`), scritto in `<DATA_DIR>/hooks` a ogni avvio e attivato con `-c core.hooksPath` sul solo `receive-pack`: dentro ai repo non si scrive niente. Soglie e branch protetto arrivano all'hook con variabili `GITSTACK_RULE_*` (le `GIT_*` si tolgono). I rifiuti escono su stderr e il client li mostra come `remote: gitstack: ...`.

- **R6, limite per file**: un push con un blob oltre `GITSTACK_GIT_MAX_FILE_SIZE` (100 MB di default) è rifiutato per intero; il messaggio nomina i file (fino a 5) con percorso e dimensione. Un file esattamente alla soglia passa. Oltre `GITSTACK_GIT_REPO_SIZE_WARN` (5 GB) il push passa con un avviso. Ogni push è controllato da git con `receive.fsckObjects=true` (F18): un oggetto malformato lo fa rifiutare. Nessuna quota per organizzazione.
- **R9, branch principale**: se la protezione del repo è attiva (default; core la dà in `protectDefaultBranch`, il branch in `defaultBranch`) force-push (non fast-forward) ed eliminazione del branch principale sono rifiutati; gli altri branch sono liberi. Con la protezione spenta sono accettati (`receive.denyDeleteCurrent=ignore`: la decide GitStack, non il default di git).
- **PR4, «Richiedi una Pull Request» (GIT-264)**: se core dà `requirePullRequest: true` nel repo (impostazione di admin, spenta di default), `access.RepoRef.RequirePullRequest` è vero e `receiverules.Rules.RepoEnv` passa il branch principale all'hook con `GITSTACK_RULE_REQUIRE_PR`: ogni push su `refs/heads/<principale>` (anche fast-forward, force-push ed eliminazione) è rifiutato, **admin compresi**, con un messaggio che spiega di aprire una Pull Request e il codice `pull_request_required`. Gli altri branch sono liberi. Cosa **non** passa da qui, di proposito, perché scrive i ref con `update-ref` nel repo e non con `receive-pack`: il **merge di una PR** (`POST /internal/git/repos/{id}/merge`, che decide core dopo aver controllato approvazioni e stati) e il **fetch del mirror in pull** (MP: sul repo di un mirror in pull attivo il codice lo cambia solo il mirror; ogni push è già rifiutato da MP2 e core rifiuta il merge delle PR con `read_only_mirror`). Il **mirror in push (V8)** parte da questo repo verso la destinazione con `git push`: legge soltanto i ref di questo repo e non è fermato dall'impostazione; la destinazione ha le regole sue. Provato in `internal/smarthttp/require_pr_test.go` e nello stack (`TestGitClientReale/PR4_richiedi_pull_request`).
- **R10, repo archiviato**: rifiutato prima di git da `access.Authorizer` (`ErrArchived`: 403 su HTTPS, uscita 1 su SSH).
- **Mirror in pull in sola lettura (MP2, GIT-198)**: se core dà `mirror.origin` nel repo (mirror in pull attivo, anche in pausa), `access.RepoRef.MirrorOrigin` è valorizzato e `receiverules.Rules.RepoEnv` passa l'origine all'hook con `GITSTACK_RULE_MIRROR_ORIGIN`: ogni push (anche eliminazioni di ref) è rifiutato con exit 1, su HTTPS e SSH, prima di guardare i ref. Messaggio: `remote: gitstack: push rifiutato: questo repo è una copia di <origine> (mirror in pull): il codice si cambia all'origine.` più una riga con il codice documentato **`mirror_read_only`**. Lo aggiorna solo il motore del mirror (`mirror-pull`, che non passa da `receive-pack`). Dopo «Interrompi» (MP6) core non dà più `mirror` e il push riesce subito: nessuno stato in git. Il rifiuto sta nell'hook e non in `access.Authorizer` perché git mostra `remote: ...` solo se la richiesta arriva fino a `receive-pack` (con un 403 HTTP il client stampa solo il codice). L'API non ha operazioni che scrivono contenuto di codice, quindi non c'è altro da bloccare; issues, etichette, milestone e commenti restano normali.
- **Git LFS (R8)**: l'hook guarda i blob del push; i file puntatore di LFS sono di poche centinaia di byte e passano, quindi LFS si potrà aggiungere senza toccare queste regole (gli oggetti LFS andranno in un endpoint a parte, non nel push git).

## Layout su disco

```
<DATA_DIR>/repos/<id[0:2]>/<id>.git   repo attivo
<DATA_DIR>/trash/<id>.git             repo nel cestino (R2)
<DATA_DIR>/tmp/                       creazioni in corso
<DATA_DIR>/hooks/                     hook pre-receive di GitStack (R6, R9), riscritto a ogni avvio
```

`<id>` è l'UUID del repo (lo stesso di `core.resources`), validato e in minuscolo: il percorso non dipende mai da owner o nome (nemmeno dalle maiuscole del nome, R11), quindi una rinomina non sposta niente (R3). La creazione avviene in `tmp/` e il repo compare in `repos/` con un rename atomico: un errore a metà non lascia repo parziali. Cestino e ripristino sono `os.Rename`; la cancellazione definitiva rimuove solo ciò che è nel cestino. L'id resta occupato (409 alla creazione) finché il repo non è cancellato davvero.

Git gira con un ambiente ripulito dalle variabili `GIT_*`, senza configurazione di sistema né utente.

## API interna

Tag `git-internal` del contratto (D-E di GIT-63). Ogni chiamata richiede gli header `X-Gitstack-*` firmati con il segreto di servizio (pacchetto `internal/trust`, copia di quello di identity): senza firma valida, 401. Errori nel formato `{"error":{"code","message"}}`.

| Operazione | Esito |
|---|---|
| `POST /internal/git/repos` — `{repoId, name, description?, defaultBranch? (main), readme?, gitignoreTemplate?, licenseTemplate?, licenseHolder?, author {name,email}}` | 201 `{repoId, empty}`; 400 (UUID, modello sconosciuto, branch o autore non validi); 409 se esiste già (anche nel cestino) |
| `GET /internal/git/repos/{repoId}` | 200 `{repoId, trashed, empty, branches}` (`empty`: nessun ref sotto `refs/heads`; `branches`: i branch esistenti in ordine alfabetico, per il branch principale di core, R4); 404 |
| `POST /internal/git/repos/{repoId}/trash` | 204; 404; 409 se è già nel cestino |
| `POST /internal/git/repos/{repoId}/restore` | 204; 404; 409 se non è nel cestino |
| `DELETE /internal/git/repos/{repoId}` | 204, solo dal cestino; 409 se il repo è attivo; 404 |

Contenuto iniziale (R5): se richiesto, un solo commit su `defaultBranch` con `README.md`, `.gitignore` e `LICENSE`, costruito senza worktree (`hash-object -w`, `mktree`, `commit-tree`, `update-ref`); autore e committer sono `author`. `author` è obbligatorio quando c'è almeno un file. I modelli (id di `.gitignore` e licenze, testi e provenienza) sono nel pacchetto `internal/templates` (vedi il suo README); un id sconosciuto dà 400. Il titolare della licenza è `licenseHolder`, altrimenti il nome dell'autore.

## Letture sulla storia (M-04/C)

Sempre nell'API interna, sempre per `repoId` (nessun utente, nessun permesso: li applica core; gli autori non portano `user`). `ref` è obbligatorio ed è un branch, un tag o uno sha (completo o prefisso di almeno 7 cifre); se un nome è sia branch sia tag vince il branch. Un repo nel cestino non si legge (404). Errori: 400 `invalid_ref` / `invalid_path` / `invalid_request`, 404 `ref_not_found` / `not_found`, 400 `blame_unavailable`.

| Operazione | Esito |
|---|---|
| `GET …/repos/{repoId}/commits?ref&author&path&page&perPage` | 200 `CommitList`: commit raggiungibili da `ref`, dal più recente; `author` (nome o email, sottostringa, senza distinguere maiuscole, non è una regex); `path` = History di un file o di una cartella (B4); `perPage` default 30, massimo 100 (oltre 400); `hasMore` senza totale |
| `GET …/commits/{sha}?ignoreWhitespace` | 200 `CommitDetail`: messaggio completo, autore e committer, genitori, `tags` che lo puntano (anche annotati), diff per file con `status` (added, modified, deleted, renamed), righe aggiunte e tolte, `binary` (senza patch) e `patch` (solo hunk). Il diff è contro il **primo genitore** (merge compresi) o contro l'**albero vuoto** per il commit iniziale |
| `GET …/commits/{sha}/diff` e `…/patch` (`?ignoreWhitespace`) | Diff completo scaricabile, in streaming (nessun buffer in memoria, timeout di 5 minuti): `.diff` = `git diff --binary`, `.patch` = `git format-patch --stdout` (per un merge: messaggio in forma di email e diff contro il primo genitore). Entrambi li accetta `git apply`. `Content-Disposition: attachment; filename="<sha12>.diff|.patch"`, `text/plain`, `nosniff`. Gli errori escono come JSON prima del primo byte |
| `GET …/blame?ref&path` | 200 `Blame`: intervalli di righe con il commit (sha, autore, data, oggetto) che le ha scritte per ultime. File oltre **1 MB** (1 048 576 byte) o binario: 400 `blame_unavailable`; percorso inesistente o cartella: 404 |

### Limiti del diff (B6)

Costanti in `internal/gitread/diff.go`:

- **File chiuso di default** (`collapsed: true`, con `collapseReason`): più di **500** righe cambiate (aggiunte + tolte) → `large`; file di lock → `lock`; file generati o minificati → `generated`. Lock e generated vincono su large. Il patch c'è comunque (salvo i limiti sotto): la UI decide se aprirlo.
  - Lock (nome del file, senza distinguere maiuscole): `package-lock.json`, `npm-shrinkwrap.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lock`, `bun.lockb`, `go.sum`, `go.work.sum`, `Cargo.lock`, `composer.lock`, `Gemfile.lock`, `poetry.lock`, `Pipfile.lock`, `uv.lock`, `pdm.lock`, `packages.lock.json`, `gradle.lockfile`, `pubspec.lock`, `mix.lock`, `Podfile.lock`, `flake.lock`, `deno.lock`.
  - Generati (suffisso): `.min.js`, `.min.mjs`, `.min.css`, `.js.map`, `.css.map`, `.pb.go`, `.pb.gw.go`, `_pb2.py`, `_pb2_grpc.py`, `.pb.cc`, `.pb.h`, `.designer.cs`, `.g.cs`, `.g.dart`, `.freezed.dart`.
- **Solo l'elenco**: oltre **300** file o **20 000** righe cambiate in totale, `listOnly: true` e `truncated: true`; i file (al massimo i primi 300, `filesChanged` è il numero reale) portano righe aggiunte e tolte ma niente `patch`.
- Un patch di un singolo file oltre **1 MB** è troncato a fine riga (`truncated: true`); oltre 32 MB di diff in memoria si ricade nel solo elenco.
- **Ignora spazi**: `ignoreWhitespace=true` usa `git diff -w`; i file che cambiano solo negli spazi non compaiono. Il download con `-w` non è garantito applicabile.

## Confronto, merge ed eliminazione dei branch (GIT-260)

Per le Pull Request di core (brain [c_65ceedb0f94b3660], PR1–PR12). Tutte dietro `trust.Require`; codice in `internal/gitmerge` e `internal/gitread/compare.go`.

- `GET /internal/git/repos/{repoId}/compare?base&head[&ignoreWhitespace]`: confronto `base...head` (branch, tag o sha). Risposta: `mergeBase` (vuoto se le storie non sono correlate), `aheadBy`, `behindBy`, `commits` (quelli di head non in base, al massimo 250, `commitsTruncated`), il diff dal merge-base con i limiti di B6 (`files`, `filesChanged`, `additions`, `deletions`, `truncated`, `listOnly`) e `mergeability` (se base è un branch). 404 `ref_not_found`.
- `GET …/mergeability?base&head`: `state` = `clean` (base è antenato di head), `behind` (head indietro ma unibile senza conflitti), `conflicts` (con `conflicts`: elenco dei file), `empty` (head già in base), `unrelated` (nessun antenato comune); `mergeable` è true solo per `clean` e `behind`. `base` deve essere un branch.
- `POST …/merge`: corpo `base` (branch), `head` (branch o sha), `method` (`merge` | `squash`; niente rebase, PR3), `message` (obbligatorio per lo squash; per il merge, se vuoto, «Merge branch 'head' into base»), `expectedBaseSha` (**obbligatorio**, sha completo), `expectedHeadSha` (facoltativo), `author` e `committer` (`name`, `email`; il committer vale l'autore se manca), `owner`, `name`, `defaultBranch` e `pusher` (`id`, `username`, `type`) per l'evento. Il merge non usa un checkout: `merge-tree --write-tree` → `commit-tree` (genitori: base e head per `merge`, solo base per `squash`, stesso albero) → `update-ref <ref> <nuovo> <atteso>`, che sposta il ref solo se punta ancora allo sha atteso. 200 con `sha`, `parents`, `tree`, `baseSha`, `headSha`, `mergeBase`. Errori: 409 `stale` (con `ref` e `currentSha`: nulla è cambiato), 409 `conflicts` (con `files`), 409 `nothing_to_merge`, 409 `unrelated_histories`, 400 `invalid_request`, 404.
- `DELETE …/branches` con corpo `branch`, `expectedSha` (facoltativo) e gli stessi campi dell'evento: 200 con lo sha che il branch aveva; 409 `default_branch` per il branch di HEAD, 409 `stale`, 404.
- **Eventi**: merge ed eliminazione pubblicano `git.push` con lo stesso Notifier dei push (`BeginAs`/`Done`): ref `refs/heads/<base>`, `before`/`after`, commit nuovi (il merge commit col suo messaggio, quindi `Closes #n` funziona, e i commit di head), `pusher` indicato da core. Le scritture sullo stesso repo sono serializzate in-process perché l'evento è una foto dei ref prima e dopo; la correttezza del ref non dipende da questo, la dà `update-ref`.
- **Regole del push**: il merge aggiunge un commit sopra lo sha di base (mai force-push) e non elimina ref, quindi R9 non lo blocca; il branch principale non si elimina. Gli altri controlli (repo archiviato R10, mirror in pull MP2, permessi) restano a core, che non deve chiamare queste rotte su un repo in sola lettura. La soglia R6 sui blob non si riapplica: il merge non porta oggetti da fuori, solo risultati di merge testuali.

## Letture del codice (M-04/B)

Nello stesso pacchetto `internal/gitread` (file `tree.go`, `file.go`, `refs.go`), con le stesse regole di `ref`, errori e permessi della sezione precedente. Costanti in `tree.go` (le stesse di `x-code-read-limits` in `api/openapi.yaml`).

| Operazione | Esito |
|---|---|
| `GET …/tree?ref&path` | 200 `Tree`: voci della cartella (radice se `path` manca) con modo, dimensione (solo file) e **ultimo commit** per voce (un `git log -1` per voce, 8 in parallelo). Cartelle prima, poi il resto, per nome; al massimo **1 000** voci, oltre `truncated: true`. `path` inesistente o file: 404 `not_found` |
| `GET …/contents?ref&path` | 200 `FileContent` con la regola **B1**: testo fino a **1 MB** (1 048 576 byte) intero, `display: highlight`; da 1 a **5 MB** (5 242 880) intero, `plain`; oltre 5 MB nessun `content`, `display: download`, `truncated: true`. Immagini PNG, JPEG, GIF, WebP e SVG riconosciute dal **contenuto** (magic number; SVG dal testo): `kind: image`, `mimeType`, `base64` e `display: image` fino a 1 MB, oltre `download`. Binario = un NUL nei primi 8 000 byte o testo non UTF-8: `kind: binary`, `download`. `size` è sempre la dimensione reale |
| `GET …/readme?ref&path` | 200 `FileContent` del `README.md`, `README` o `README.txt` della cartella (in quest'ordine, senza distinguere maiuscole); 404 se non c'è |
| `GET …/raw?ref&path` | I byte del file in streaming (`git cat-file blob`), `Content-Length` noto. **Regola B3**: testo non SVG → `text/plain; charset=utf-8` in linea; ogni altro file, SVG compreso → `application/octet-stream` con `Content-Disposition: attachment`. Sempre `X-Content-Type-Options: nosniff` e `Content-Security-Policy: sandbox`, scritti qui (non da nginx né da core). Mai `text/html` né `image/svg+xml`. Il tipo si decide prima del primo byte: gli errori escono come JSON |
| `GET …/branches` | 200 `BranchList`: il branch di `HEAD` per primo (`isDefault`), gli altri dal più recente; `protected` sempre false (lo imposta core); repo vuoto: lista vuota |
| `GET …/tags` | 200 `TagList` dal più recente. **B7**: per un annotato `annotated: true`, `message` e `taggedAt` del tag; per uno leggero `taggedAt` è la data del commit. `commit` è sempre il commit puntato. I tag che non puntano a un commit sono saltati. Gli indirizzi `zipUrl`/`tarGzUrl` li aggiunge core |
| `GET …/archive?ref&format&name` | ZIP (default) o `tar.gz` dell'albero di branch, tag o commit con `git archive`, in streaming (timeout di 5 minuti). `name` (nome del repo, obbligatorio) compone il file `<name>-<ref>.zip\|.tar.gz` e la cartella radice `<name>-<ref>/` dentro l'archivio. `format` diverso o `name` non valido: 400 `invalid_request`. `attachment`, `application/zip` o `application/gzip`, stessi header di sicurezza del raw |

Per le letture che scorrono i file di un ref (GIT-83, lingue) c'è `Service.ListTree(ctx, repoID, ref, dir, recursive)`: voci `ls-tree -l` con modo, tipo, sha, dimensione in byte e percorso dalla radice.

### Sicurezza dei comandi

Ogni comando git passa da `internal/gitrun`: ambiente senza `GIT_*`, niente configurazione di sistema o utente, `--literal-pathspecs`, un timeout (30 s; 5 minuti per lo streaming) e un tetto sull'output tenuto in memoria. `internal/gitref` valida ref (niente `..`, spazi, caratteri di controllo, `~^:?*[\`, `@{`, né un `-` iniziale), percorsi (relativi, senza segmenti `.` o `..`) e sha prima di toccare git, e risolve ref e prefissi di sha in uno sha completo di commit: a git arrivano solo sha esadecimali, i percorsi sempre dopo `--`. `gitref` è il pacchetto da riusare per le altre letture.

## Test

`go test ./...` da `services/git` usa un `git` reale (`t.Skip` se manca dal PATH) e directory temporanee.

## Mirror in push (V8, GIT-179)

`POST /internal/git/repos/{repoId}/mirror-push` (dietro `trust.Require`, lo chiama il motore dei mirror di core): spinge il **branch principale** e i **tag** del repo su un remote HTTPS. Corpo: `url`, `username`, `token`, `ip`, `defaultBranch`. Risposta 200 con `ok`, `diverged`, `refs` (per ref: `pushed`, `up-to-date`, `rejected`, `error`, con SHA locale e motivo), `error` (ripulito) e `durationMs`; 400 su input non valido, 404 se il repo non c'è, 409 se un push per lo stesso mirror è già in corso. Codice in `internal/mirrorpush`.

- **Mai forzato.** Il comando è `git push --porcelain <url> refs/heads/<principale>:refs/heads/<principale> 'refs/tags/*:refs/tags/*'`: nessun `--force`, nessun `+` nei refspec, nessuna cancellazione di ref (un refspec con sorgente vuota non si costruisce mai). Si interpreta l'output porcelain: `!` con `(non-fast-forward)`, `(fetch first)` o `(already exists)` (un tag già presente con un altro commit) è **divergenza**: la destinazione non si tocca. Un flag `+` o `-` che comparisse comunque è trattato come errore, mai come successo.
- **Credenziale solo nell'ambiente.** L'utente e il token non stanno mai nell'URL (che non può avere userinfo) né in argv: passano da `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.extraHeader`, `GIT_CONFIG_VALUE_0='Authorization: Basic …'` nell'ambiente del solo processo git, con `GIT_TERMINAL_PROMPT=0` e `credential.helper` vuoto. Lo stderr di git è ripulito da token, credenziale base64 e righe `Authorization` prima di finire nella risposta o nei log; il log dell'endpoint riporta solo repo, esito e numero di ref.
- **Rete.** Core controlla la destinazione con `pkg/egress` (C8) e passa l'IP scelto: git lo usa con `http.curloptResolve=<host>:<porta>:<ip>` (richiede git ≥ 2.37, l'immagine Alpine 3.22 lo ha), quindi un cambio di risposta DNS dopo il controllo non sposta la connessione. `http.followRedirects=false`, `protocol.allow=never` salvo `https`. Proxy (N9): senza `HTTPS_PROXY` nell'ambiente del servizio nessun proxy (`NO_PROXY=*`, `-c http.proxy=`); con un proxy valido per la destinazione (`HTTPS_PROXY`/`NO_PROXY`, come `http.ProxyFromEnvironment`) git esce dal proxy con `http.proxy` passato da `GIT_CONFIG_*` (mai in riga di comando, credenziali comprese) e l'IP fissato non si usa: core ha già risolto e giudicato ogni indirizzo prima di chiamare git. Vale anche per il mirror in pull (`mirrorpull`). L'immagine include `ca-certificates`: una destinazione con un certificato non attendibile fallisce (nessuna opzione per saltare il controllo TLS). Per una destinazione con il certificato di una CA privata (un secondo GitStack con la CA interna) `GITSTACK_GIT_MIRROR_CA_FILE` indica un file PEM: git lo usa come `GIT_SSL_CAINFO` per i soli push dei mirror e **sostituisce** le CA di sistema per quei push (per spingere anche su GitHub il file deve contenerle tutte). Nel chart: `git.mirror.caConfigMap` (GIT-181).
- **Concorrenza e tempo.** Un mutex per (repo, destinazione): un secondo push concorrente è 409. Timeout di 2 minuti (`mirrorpush.DefaultTimeout`), poi il processo è fermato (`gitrun.RunEnv` con `WaitDelay`, che non aspetta i figli di git-remote-https).
- **Test.** `internal/mirrorpush`: parser del porcelain, argv senza token, un secondo server git vero come destinazione (`httptest.NewTLSServer` + `net/http/cgi` su `git http-backend`, CA passata solo nei test con `GIT_SSL_CAINFO`): push di branch e tag con lo stesso SHA, divergenza di un branch e di un tag senza sovrascrittura, errore del server senza il token, un solo push per mirror e timeout. `internal/httpserver/mirror_test.go`: autorizzazione, input e token assente da risposta e log.

## Mirror in pull (M-09/A2, GIT-196)

`POST /internal/git/repos/{repoId}/mirror-pull` (dietro `trust.Require`, lo chiama il motore dei mirror di core): aggiorna il repo nudo da un'origine HTTPS in modo prudente (brain [c_4e8670cfd950d497], MP1–MP8). Corpo: `url`, `username`, `token` (entrambi o nessuno per un'origine pubblica), `ip`, `defaultBranch`, `owner`, `name`, `pusher` (`id`, `username`: l'utente «mirror» che firma l'evento). Risposta 200 con `ok`, `diverged`, `refs` (per ref: `created`, `updated`, `up-to-date`, `diverged` con `reason` `non-fast-forward`, `tag-moved`, `deleted-at-origin`, `changed-locally`), `headBranch` (solo al primo giro su un repo vuoto), `changed`, `error` (ripulito) e `durationMs`; 400 su input non valido, 403 su destinazione vietata, 404 se il repo non c'è, 409 se un aggiornamento per lo stesso repo è già in corso. Codice in `internal/mirrorpull`.

- **Mai sovrascrivere (MP1).** Il fetch scrive solo in `refs/mirror-incoming/` (svuotato prima e dopo, refspec senza `+`); poi un ref si crea o un branch avanza solo in fast-forward, con `update-ref` che controlla il valore precedente. Un tag con altro commit, un branch riscritto o rimasto indietro e un ref cancellato nell'origine restano come sono e vanno in `diverged`; gli altri ref si aggiornano comunque. Niente `--force`, `--prune`, `+`.
- **Credenziale e rete.** Come il push: `http.extraHeader` solo da `GIT_CONFIG_*` nell'ambiente, `credential.helper=` vuoto, `GIT_ASKPASS=` vuoto, `http.curloptResolve` sull'IP scelto da core, nessun redirect né proxy, redactor di `mirrorpush` su ogni testo in uscita; CA private da `GITSTACK_GIT_MIRROR_CA_FILE`. Il servizio non applica la policy completa di `pkg/egress` (liste e CIDR del cluster li ha core): rifiuta comunque loopback, link-local, non specificato e multicast (`mirrorpull.DefaultCheckIP`) prima di ogni connessione.
- **Concorrenza, tempo, eventi.** Un fetch alla volta per repo (409), timeout di 10 minuti (`mirrorpull.DefaultTimeout`). Dopo l'aggiornamento il `pushevent.Notifier` pubblica `git.push` per i ref cambiati (`BeginAs`, con il pusher indicato nel corpo). Repo vuoto: HEAD punta al branch di HEAD dell'origine (`ls-remote --symref`) e `headBranch` lo dice a core.
- **Test.** `internal/mirrorpull`: origine reale (`git http-backend` dietro `httptest.NewTLSServer`): copia di branch e tag, force-push, cancellazione e tag spostato lasciano i ref e finiscono in `diverged`, argv e ambiente senza credenziali, destinazioni vietate senza connessione, errore del server senza token, lock per repo, timeout, origine vuota e pubblica. `internal/httpserver/mirrorpull_test.go`: autorizzazione, input, 403 e token assente da risposta e log.
