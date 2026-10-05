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

Probe: `GET /healthz` (liveness, sempre `ok`) e `GET /readyz` (503 se `DATA_DIR` non esiste o non è scrivibile).

## Server SSH (M-03/I, R7)

Porta **2222** di default (`GITSTACK_GIT_SSH_ADDR`, nel chart `git.ssh.port`); l'installer non tocca mai l'SSH dell'host. Indirizzo: `ssh://git@<host>:2222/<owner>/<repo>.git`; la forma corta `git@<host>:<owner>/<repo>.git` vale solo con porta 22.

- Solo chiave pubblica, login `git`. Il fingerprint `SHA256:...` della chiave si risolve con identity `GET /internal/ssh-keys/{fingerprint}` (percorso con `url.PathEscape`); chiave sconosciuta o utente disattivato: rifiuto.
- Nessuna shell, pty, subsystem o forwarding. Si accettano solo `git-upload-pack` e `git-receive-pack` (anche `git upload-pack`/`git receive-pack`) su `[/]<owner>/<repo>[.git]`, senza argomenti extra, senza metacaratteri; ogni altro comando esce con errore 128.
- Permessi: core `GET /repos/{owner}/{repo}` con l'identità firmata dell'utente (404 per «non esiste» e «non puoi leggerlo», stesso messaggio) e identity `POST /internal/permissions/check`: `read` per upload-pack, `write` per receive-pack; un repo archiviato rifiuta il push. È lo stesso `access.Authorizer` dello smart HTTP (`write=true` per receive-pack), con un solo `upstream.Client`; una chiave SSH vale come l'utente intero (scope read e write), restano ruolo e archiviazione. Il repo archiviato (R10) si legge ma rifiuta il push, anche via HTTPS (403) e via SSH (`access.ErrArchived`). La chiave si risolve con `upstream.Client.LookupKey`.
- Chiave host: vedi la variabile sopra; non cambia a ogni riavvio, quindi i client non vedono «host key changed».
- `internal/sshd`; i test usano un client `ssh`/`git` reale (`t.Skip` se mancano dal PATH).

## Layout su disco

```
<DATA_DIR>/repos/<id[0:2]>/<id>.git   repo attivo
<DATA_DIR>/trash/<id>.git             repo nel cestino (R2)
<DATA_DIR>/tmp/                       creazioni in corso
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

## Test

`go test ./...` da `services/git` usa un `git` reale (`t.Skip` se manca dal PATH) e directory temporanee.
