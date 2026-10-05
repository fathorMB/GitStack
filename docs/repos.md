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
- `GET /internal/git/repos/{repoId}` → `{repoId, trashed, empty}`;
- `POST .../trash` e `POST .../restore` → 204;
- `DELETE /internal/git/repos/{repoId}` → 204, solo dal cestino, altrimenti 409.

Id dei modelli (stabili): gitignore `go, node, python, java, dotnet, rust, cpp,
terraform, ruby, php`; licenza `mit, apache-2.0, gpl-3.0, agpl-3.0, lgpl-3.0,
mpl-2.0, bsd-2-clause, bsd-3-clause, unlicense`.

## Letture del codice (M-04, GIT-80)

Contratto in `api/openapi.yaml`: operazioni di lettura nel tag `repos` sotto
`/repos/{owner}/{repo}` (albero `tree`, file `contents`, `raw`, `branches`,
`tags`, `commits`, `commits/{sha}`, `blame`, `archive`, `languages`,
`readme`), le corrispondenti interne nel tag `git-internal` sotto
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

### Limiti (costanti documentate, nel contratto come `maximum`/descrizioni)

| Cosa | Limite | Oltre |
|---|---|---|
| vista di un file (`contents`, `readme`) | 1 MB (1 048 576 byte) | `truncated`: `content` ha il primo MB; `size` è quella reale; `raw` non ha limite |
| diff di un commit | 300 file, 20 000 righe totali, 1 MB per file | `truncated` sul file o sulla risposta, patch omesso o parziale |
| storico (`commits`) | pagina di default 30, massimo 100 | `hasMore`, senza totale (costerebbe un giro completo) |
| albero (`tree`) | 1 000 voci per cartella | `truncated` |
| blame | file di testo fino a 1 MB | 400 `blame_unavailable` |

*Motivo:* tengono limitati memoria e tempo di una richiesta; i file più grandi
si scaricano con `raw` o `archive`, che non bufferizzano.

Stato: i contratti sono fissati da GIT-80; gli handler pubblici di core
rispondono 501 fino a GIT-84, l'API interna del servizio git la scrivono GIT-81
e GIT-82 (a mano, D-E) e `lookup-emails` in identity risponde 501 finché non
è implementato.
