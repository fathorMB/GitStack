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
