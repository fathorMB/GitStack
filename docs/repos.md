# Repo: scelte di contratto (M-03/A, GIT-63)

Contratto in `api/openapi.yaml`: tag `repos` (API pubblica, servita da core),
`git-internal` (API interna del servizio git) e, nel tag `internal`, le
operazioni di identity `setResourceAttributes` e `resolveOwner`. Regole di
prodotto: R1–R12 (`.prisma/knowledge/topics/repository-git.md`) e P1–P7
(`identita-e-sicurezza.md`). Lo schema di core è in
[services/core/README.md](../services/core/README.md).

## Scope dei token (D-B)

Nessuno scope nuovo: il catalogo resta di 7. Un repo è una risorsa, quindi:

| Operazione | Scope | Ruolo sul repo |
|---|---|---|
| leggere, elencare, clone, fetch | `read:resource` | `read` |
| creare | `write:resource` | creare repo per l'owner scelto |
| push | `write:resource` | `write` |
| impostazioni, archiviazione, eliminazione, ripristino | `write:resource` | `admin` (R12) |

*Motivo:* i token già emessi continuano a funzionare e non si toccano
identity, gateway né la UI dei token. Dopo la v1 si potranno introdurre
`read:repo`/`write:repo` come alias. Sui percorsi `/repos` non c'è
`x-required-permission` (non hanno `resourceId`): il permesso lo applica core.

Errori: un repo che l'utente non può leggere risponde 404 (non 403, per non
rivelarne l'esistenza); 403 se lo legge ma non ha il ruolo; 409 per un nome
occupato (anche da un repo eliminato) e per modifiche a un repo archiviato.

## Owner e visibilità in identity (D-C)

Identity tiene owner e visibilità in una tabella propria (la crea GIT-65).
Core la aggiorna con `PUT /internal/resources/{resourceId}/attributes`, body
`{ownerType: user|organization, ownerId, visibility: private|internal}`,
risposta 204, idempotente; la chiama alla creazione, prima di
`grantResourceCreator`, e a ogni cambio di visibilità. L'owner non cambia mai
(R3). Una risorsa senza attributi si comporta come prima.
`GET /internal/owners/{name}` → `{type, id, name}` (404 se il nome non esiste)
risolve lo spazio di nomi unico (R1).

*Motivo:* così `checkPermission` e `listReadableResources` calcolano da sole
P1, P3 e P6 e il servizio git chiede solo per `resourceId`. Finché non arriva
GIT-65, identity risponde 501 a entrambe.

## API interna del servizio git (D-E)

Tag `git-internal`, `serviceAuth`, implementata a mano da GIT-66 (non
generata: il tag è escluso dagli `oapi-codegen.yaml` di core e gateway,
identity usa `include-tags`; il gateway non lo espone mai):

- `POST /internal/git/repos` → 201 `{repoId, empty}` (409 se esiste, 400 per
  un modello sconosciuto);
- `GET /internal/git/repos/{repoId}` → `{repoId, trashed, empty, branches}` (`branches` aggiunto da GIT-67 per validare il branch principale, R4);
- `POST .../trash` e `POST .../restore` → 204;
- `DELETE /internal/git/repos/{repoId}` → 204, solo dal cestino, altrimenti 409.

Id dei modelli (stabili): gitignore `go, node, python, java, dotnet, rust, cpp,
terraform, ruby, php`; licenza `mit, apache-2.0, gpl-3.0, agpl-3.0, lgpl-3.0,
mpl-2.0, bsd-2-clause, bsd-3-clause, unlicense`.

## Letture del codice (M-04, GIT-80, GIT-91)

Contratto in `api/openapi.yaml`: operazioni di lettura nel tag `repos` sotto
`/repos/{owner}/{repo}` (albero `tree`, file `contents`, `raw`, `branches`,
`tags`, `commits`, `commits/{sha}`, `commits/{sha}/patch`, `blame`, `archive`,
`languages`, `readme` e, da GIT-91, `raw/{refAndPath}`, `files` e `search`), le corrispondenti interne nel tag `git-internal` sotto
`/internal/git/repos/{repoId}/...` e, nel tag `internal` di identity,
`POST /internal/users/lookup-emails`. Mockup 07–10.

### Le letture passano da core

Core risolve owner/nome → repoId, applica il permesso `read` e lo scope
`read:resource` (D-B), tratta un repo eliminato come inesistente (404) e uno
archiviato come leggibile; poi chiama il servizio git per repoId. Raw e ZIP
passano in streaming (`io.Copy`, nessun buffer in memoria). Il gateway non
instrada mai verso il servizio git (D-E resta valida) e il generatore del
route table non cambia: le operazioni pubbliche sono servite da core come
quelle di M-03, le interne non entrano nel route table.

*Motivo:* un solo punto che applica i permessi e risolve i nomi, come per le
API dei repo di M-03; il costo, un salto in più per raw e ZIP, è accettato.
Le operazioni interne parlano solo per repoId, non conoscono utenti né
permessi, e vogliono sempre un `ref` esplicito (il branch principale lo
sostituisce core). Un repo che l'utente non può leggere risponde 404, non 403.

### `ref`, errori

`ref` può essere branch, tag o sha (completo o prefisso di almeno 7
caratteri); se manca vale il branch principale (R4); se un nome è sia branch
sia tag vince il branch. Errori: 401 senza credenziali; 404 per repo
inesistente, eliminato o non leggibile, per `ref_not_found` e per un percorso
inesistente; 400 `invalid_ref` per un ref sintatticamente non valido, 400
`invalid_path` per un percorso con `.`/`..` o `/` iniziale.

### Autore di un commit → utente GitStack

L'email dell'autore (e del committer) si confronta, senza distinguere
maiuscole, con `users.email` di identity, già unica su lower(email)
(`0001_init_identity_schema.up.sql`). Se c'è corrispondenza la risposta porta
`user {id, username, kind, avatarUrl}`, con `kind` `human` o `agent` (badge);
altrimenti solo `name` ed `email` del commit e `user` è null. Core chiama
`POST /internal/users/lookup-emails` (`{emails}`, massimo 100, `serviceAuth`)
una volta per pagina di storico, dettaglio o blame; il servizio git non
porta mai `user`.

*Motivo:* l'email è l'unico legame che un commit porta con sé ed è già unica.
**Come su GitHub, l'autore dichiarato in un commit non è una prova
d'identità**: chiunque può scrivere in un commit l'email di un altro. Il
badge indica «questa email appartiene a quell'utente», non «l'ha scritto
lui»; nessuna decisione di sicurezza deve basarsi su di esso.

### Limiti (costanti documentate)

Le costanti stanno in un posto solo, l'estensione `x-code-read-limits` in
testa a `api/openapi.yaml`, e i servizi le definiscono una volta con questi
nomi. Schemi e descrizioni le ripetono (`maximum`, `maxItems`, `maxLength`).

| Costante | Valore | Regola | Oltre |
|---|---|---|---|
| `fileHighlightMaxBytes` | 1 MB (1 048 576 byte) | B1 | testo da 1 a 5 MB: `display` `plain` |
| `filePlainMaxBytes` | 5 MB (5 242 880 byte) | B1 | `display` `download`, nessun `content`, `truncated` true |
| `imageInlineMaxBytes` | 1 MB | B1 | immagine oltre: `display` `download` |
| `blameMaxBytes` | 1 MB | B4 | 400 `blame_unavailable` |
| `treeMaxEntries` | 1 000 voci per cartella | M-04 | `truncated` |
| `commitsPerPageMax` | 100 (default 30) | M-04 | `hasMore`, senza totale |
| `diffCollapseLines` | 500 righe cambiate per file | B6 | file `collapsed` (`large`) |
| `diffMaxFiles` | 300 file | B6 | `listOnly`: solo l'elenco |
| `diffMaxLines` | 20 000 righe cambiate in totale | B6 | `listOnly` |
| `diffFileMaxBytes` | 1 MB di patch per file | M-04 | `truncated` sul file |
| `fileListMaxPaths` | 50 000 percorsi | B5 | `truncated` |
| `searchMaxResults` | 100 risultati | B5 | `limitReached` |
| `searchTimeoutSeconds` | 10 s | B5 | `timedOut`, risultati parziali |
| `searchFragmentMaxChars` | 300 caratteri | B5 | frammento tagliato |
| `searchFileMaxBytes` | 1 MB | B5 | il file non è cercato |

Le «righe» di un diff sono quelle cambiate (aggiunte più tolte). I valori di
`fileListMaxPaths`, `searchTimeoutSeconds`,
`searchFragmentMaxChars`, `searchFileMaxBytes` e `imageInlineMaxBytes` non sono
nelle regole del board: li ha scelti GIT-91 e si possono cambiare insieme al
contratto.

*Motivo:* tengono limitati memoria e tempo di una richiesta; i file più grandi
si scaricano con `raw` o `archive`, che non bufferizzano.

### Vista di un file (B1)

`contents` e `readme` rispondono sempre con `kind` (`text`, `image`,
`binary`) e `display`, che dice quale caso si applica:

| `display` | Quando | `content` | `truncated` |
|---|---|---|---|
| `highlight` | testo fino a 1 MB | testo intero | false |
| `plain` | testo da 1 a 5 MB | testo intero, senza evidenziazione | false |
| `image` | PNG, JPEG, GIF, WebP, SVG fino a 1 MB | base64 (`encoding` `base64`), con `mimeType` | false |
| `download` | testo oltre 5 MB, immagine oltre 1 MB, altri binari | assente | true solo per il testo oltre 5 MB |

`size` è sempre quella reale. Le immagini si riconoscono dal contenuto (magic
number; per SVG la radice `<svg`), non dall'estensione. Un SVG è sempre
un'immagine, mai una pagina: la UI lo mostra da `content` in base64 come
`<img>` (gli script in un SVG caricato come immagine non girano), mentre
`raw` lo serve come allegato. Perciò nemmeno le immagini hanno bisogno di
`raw` per essere mostrate. *Motivo:* il caso lo decide il servizio, una volta,
non la UI.

### Raw e archivi (B3)

Raw e archivi chiedono sessione o token con `read` (scope `read:resource`);
nessun link anonimo. Il raw esiste in due forme con gli stessi byte:

- `GET /repos/{owner}/{repo}/raw?ref=&path=` (`getRepositoryRaw`), per la SPA e
  i client;
- `GET /repos/{owner}/{repo}/raw/{refAndPath}` (`getRepositoryRawByPath`), che
  è l'indirizzo `/<owner>/<repo>/raw/<branch|tag|commit>/<percorso>` della
  regola B3.

**Ref con `/`**: `refAndPath` occupa più segmenti. Core prende il prefisso
più lungo che è il nome di un branch, altrimenti di un tag; se nessuno
corrisponde, il primo segmento è uno sha (completo o prefisso di almeno 7
caratteri) e il resto il percorso. Se un nome è sia branch sia tag vince il
branch. Nessun corrispondente: 404 `ref_not_found`. Nel contratto l'operazione
porta `x-path-tail: refAndPath`: il router di core (`{refAndPath...}` nel
`ServeMux`) e la tabella del gateway devono trattarla come coda su più
segmenti, non come un segmento solo. Lo scrive GIT-81 insieme all'handler: il
codice generato oggi la vede come un segmento, ed è inoffensivo perché
l'handler risponde 501.

**Intestazioni di sicurezza** (le scrive il servizio che risponde, core o git,
**non** nginx, così valgono anche passando da `/api/v1`; nel contratto sono le
`headers` delle risposte `RawFile`, `Archive` e `Patch`):

| Caso | `Content-Type` | `Content-Disposition` |
|---|---|---|
| file di testo (non SVG, non binario) | `text/plain; charset=utf-8` | assente (in linea) |
| ogni altro file: binari, immagini, **SVG compreso** | `application/octet-stream` | `attachment; filename="<nome>"` |
| archivio | `application/zip` o `application/gzip` | `attachment`, `<repo>-<ref>.zip` o `.tar.gz` |
| diff e patch | `text/plain; charset=utf-8` | `attachment`, `<sha12>.diff` o `<sha12>.patch` (nome dato dal servizio git) |

In tutti i casi: `X-Content-Type-Options: nosniff` e
`Content-Security-Policy: sandbox`. Mai `text/html`, `image/svg+xml` o altro
tipo che un browser eseguirebbe. Un test di GIT-81 deve verificare queste
intestazioni su un file HTML e su un SVG con uno script.

**Archivio** (`getRepositoryArchive`): `format` `zip` (default) o `tar.gz`, per
branch, tag o commit (`ref`), in streaming.

**Instradamento di `/<owner>/<repo>/raw/...` (decisione del CTO, rivista in GIT-91).**
L'indirizzo `/<owner>/<repo>/raw/<ref>/<percorso>` (e `/<owner>/<repo>/archive/...`,
se si espone a primo livello) arriva con un `IngressRoute` Traefik con
`PathRegexp` e un `Middleware` `ReplacePathRegex` verso il **gateway**, su
`/v1/repos/<owner>/<repo>/raw/<resto>`, non verso il servizio git. L'Ingress
`/api` → gateway e `/` → web restano come sono. *Motivi:*
1. un solo meccanismo nel chart per i percorsi di primo livello dei repo, lo
   stesso di `.git` (GIT-70, `deploy/gitstack/templates/ingress-git.yaml`);
2. nessun salto in più nell'nginx della web UI per lo streaming di file e
   archivi grandi;
3. l'autenticazione (sessione o token con `read`) e le intestazioni di
   sicurezza restano nel gateway e in core, senza un secondo punto che le
   applichi.

Il template del chart lo scrive GIT-81, insieme all'handler raw; GIT-91 non
tocca il chart.

### Cerca nei file (B5)

- `GET /repos/{owner}/{repo}/files?ref=` (`listRepositoryFiles`): i percorsi di
  tutti i file (non delle cartelle) del ref, in ordine alfabetico, al massimo
  50 000 (`truncated` se di più). La corrispondenza approssimata di «Go to
  file» la fa il client sull'elenco.
- `GET /repos/{owner}/{repo}/search?q=&ref=` (`searchRepositoryCode`): cerca `q`
  (da 2 a 256 caratteri, sottostringa letterale, senza distinguere maiuscole)
  nei file di testo fino a 1 MB del ref, senza indice. Risposta: `results` (al
  massimo 100, ognuno con `path`, `line`, `fragment`), `limitReached` se ce
  n'erano altri e `timedOut` se la ricerca è stata interrotta dopo 10 secondi
  (risultati parziali).

Entrambe passano da core come le altre letture (permesso `read`, repo non
leggibile 404); le corrispondenti interne sono `gitListFiles` e `gitSearchCode`.

### Diff di un commit (B6)

`getRepositoryCommit` porta, per ogni file, `collapsed` (chiuso di default) e il
motivo `collapseReason`: `lock` (file di lock), `generated` (file generato o
minificato) o `large` (oltre 500 righe cambiate); `lock` e `generated` prevalgono
su `large`. Il patch c'è comunque, salvo i limiti sotto: la UI decide se
aprirlo («Load diff»). Gli schemi e le operazioni interne sono quelli di GIT-82
(`FileDiff`, `CommitDetail.listOnly`, `CommitDetail.ignoreWhitespace`,
`CommitDetail.tags`, `gitGetCommit`, `gitGetCommitDiff`, `gitGetCommitPatch`).

**File di lock e generati:** l'elenco completo, per nome, è in
`services/git/README.md` (sezione del diff), che è la fonte e il servizio git
ripete in una costante. Per esempio, lock: `package-lock.json`, `pnpm-lock.yaml`,
`yarn.lock`, `go.sum`, `Cargo.lock`; generati: `*.min.js`, `*.min.css`,
`*.pb.go`.

Oltre 300 file o 20 000 righe cambiate in totale `listOnly` è true: la risposta
porta solo l'elenco dei file con righe aggiunte e tolte, senza `patch`. Resta il
limite di 1 MB di patch per file (`truncated`). `ignoreWhitespace` calcola il
diff con `git diff -w`. `path` (solo pubblico) restringe `files` a quel file: lo
fa core sulla risposta del servizio git, quindi con `listOnly` il patch non c'è
e si usa il download. Il diff completo, senza limiti e in streaming, si scarica
con `GET /repos/{owner}/{repo}/commits/{sha}/patch?format=diff|patch`
(`getRepositoryCommitPatch`, anche con `ignoreWhitespace`): core lo ottiene da
`gitGetCommitDiff` (`git diff --binary`) o `gitGetCommitPatch`
(`git format-patch`) e lo passa in streaming con le intestazioni di sicurezza
sopra; un commit senza genitori è confrontato con l'albero vuoto, un merge con il
primo genitore.

### Storico per file (B4)

Esiste già: `getRepositoryCommits` ha il parametro `path` (solo i commit che
toccano quel percorso), verificato in GIT-91. Il blame è `getRepositoryBlame`
(fino a 1 MB di testo).

### Tag (B7)

`getRepositoryTags` porta per ogni tag `taggedAt` (la data del tag annotato,
quella del commit per un tag leggero), `commit`, `annotated`, `message` (solo se
annotato) e gli indirizzi di scaricamento `zipUrl` e `tarGzUrl` (relativi a
`/v1`, di `getRepositoryArchive` con `ref` il nome del tag). Li aggiunge core: il
servizio git non conosce owner e nome. Solo tag nella v1; le release sono in v2.

Stato (GIT-84): gli handler pubblici di core sono implementati (albero, file,
README, raw nelle due forme, branch, tag, storico, dettaglio, blame, archivi,
`.diff` e `.patch`); `lookup-emails` in identity collega gli autori ai
utenti. Restano 501 `getRepositoryLanguages` (GIT-83) e `listRepositoryFiles` e
`searchRepositoryCode` (GIT-93). Core chiama git con `gitclient.Reader`
(JSON bufferizzato per le risposte già limitate, streaming per raw, archivi e
patch); l'accesso è `codeAccess` in `repos_code.go`. La coda `{refAndPath...}` è
registrata a mano in core (`router.go`) e nel gateway (`mountTail`, e
`tailParams` nella tabella di sicurezza), perché il codice generato la vede
come un segmento solo.
