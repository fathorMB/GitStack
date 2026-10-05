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
| `GITSTACK_IDENTITY_SERVICE_SECRET` | — | Segreto di servizio, lo stesso di core e identity. Senza, ogni chiamata a `/internal/*` è 401. Non finisce mai nei log. |
| `GITSTACK_GIT_MAX_FILE_SIZE` | `100MB` | Dimensione massima di un file (blob) in un push (R6). Numero di byte o con suffisso `KB`, `MB`, `GB` (multipli di 1024: 100MB = 104 857 600 byte); `0` = nessun limite. |
| `GITSTACK_GIT_REPO_SIZE_WARN` | `5GB` | Oltre questa dimensione del repo il push è accettato con un avviso (R6). Stesso formato; `0` = nessun avviso. |

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

- **R6, limite per file**: un push con un blob oltre `GITSTACK_GIT_MAX_FILE_SIZE` (100 MB di default) è rifiutato per intero; il messaggio nomina i file (fino a 5) con percorso e dimensione. Un file esattamente alla soglia passa. Oltre `GITSTACK_GIT_REPO_SIZE_WARN` (5 GB) il push passa con un avviso. Nessuna quota per organizzazione.
- **R9, branch principale**: se la protezione del repo è attiva (default; core la dà in `protectDefaultBranch`, il branch in `defaultBranch`) force-push (non fast-forward) ed eliminazione del branch principale sono rifiutati; gli altri branch sono liberi. Con la protezione spenta sono accettati (`receive.denyDeleteCurrent=ignore`: la decide GitStack, non il default di git).
- **R10, repo archiviato**: rifiutato prima di git da `access.Authorizer` (`ErrArchived`: 403 su HTTPS, uscita 1 su SSH).
- **Git LFS (R8)**: l'hook guarda i blob del push; i file puntatore di LFS sono di poche centinaia di byte e passano, quindi LFS si potrà aggiungere senza toccare queste regole (gli oggetti LFS andranno in un endpoint a parte, non nel push git).

## Layout su disco

```
<DATA_DIR>/repos/<id[0:2]>/<id>.git   repo attivo
<DATA_DIR>/trash/<id>.git             repo nel cestino (R2)
<DATA_DIR>/tmp/                       creazioni in corso
<DATA_DIR>/hooks/                     hook pre-receive di GitStack (R6, R9), riscritto a ogni avvio
```

`<id>` è l'UUID del repo (lo stesso di `core.resources`), validato e in minuscolo: il percorso non dipende mai da owner o nome, quindi una rinomina non sposta niente (R3). La creazione avviene in `tmp/` e il repo compare in `repos/` con un rename atomico: un errore a metà non lascia repo parziali. Cestino e ripristino sono `os.Rename`; la cancellazione definitiva rimuove solo ciò che è nel cestino. L'id resta occupato (409 alla creazione) finché il repo non è cancellato davvero.

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

### Sicurezza dei comandi

Ogni comando git passa da `internal/gitrun`: ambiente senza `GIT_*`, niente configurazione di sistema o utente, `--literal-pathspecs`, un timeout (30 s; 5 minuti per lo streaming) e un tetto sull'output tenuto in memoria. `internal/gitref` valida ref (niente `..`, spazi, caratteri di controllo, `~^:?*[\`, `@{`, né un `-` iniziale), percorsi (relativi, senza segmenti `.` o `..`) e sha prima di toccare git, e risolve ref e prefissi di sha in uno sha completo di commit: a git arrivano solo sha esadecimali, i percorsi sempre dopo `--`. `gitref` è il pacchetto da riusare per le altre letture (albero, file, branch, tag).

## Test

`go test ./...` da `services/git` usa un `git` reale (`t.Skip` se manca dal PATH) e directory temporanee.
