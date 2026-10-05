# Git via HTTPS (smart HTTP): indirizzo, token, credential helper

M-03/H (GIT-70). Regole: R1 (indirizzo), P2 (nessun accesso anonimo), P3
(interno = lettura per tutti) di `.prisma/knowledge/topics/`.

## Indirizzo

```
https://<host>/<owner>/<repo>.git
```

`<owner>` è l'utente o l'organizzazione (spazio di nomi unico, R1). L'URL di
clone si legge anche dal repo (`cloneUrls.https`, `GET /api/v1/repos/<owner>/<repo>`).

## Autenticazione

Un **token personale** (`gst_...`, creato dalle impostazioni o da
`POST /api/v1/users/<username>/tokens`) in Basic auth: la password è il token,
l'utente è qualsiasi (di solito il proprio username). Senza credenziali il
server risponde `401` con `WWW-Authenticate: Basic`, così git le chiede.
Sessioni web e password non aprono l'accesso git; un token scaduto o revocato
è rifiutato (401).

| Operazione | Scope del token | Ruolo sul repo |
|---|---|---|
| clone, fetch, pull | `read:resource` | `read` (un repo **interno** è leggibile da ogni utente, P3) |
| push | `write:resource` | `write` |

Risposte: `401` credenziale mancante o non valida; `404` repo inesistente,
eliminato, nel cestino **o non leggibile** (stessa risposta, per non rivelare
che esiste); `403` repo leggibile ma token senza `write:resource` o utente
senza ruolo `write` (push); `503` identity o core non raggiungibili (mai un
accesso aperto per un errore). Il protocollo "dumb" non è supportato.

## Configurare git (credential helper)

Memorizzare il token una volta sola (al primo clone git lo chiede: utente =
username, password = token):

```sh
# Windows (Git Credential Manager) e macOS: già attivo, niente da fare.
# Linux, in memoria per un'ora:
git config --global credential.helper 'cache --timeout=3600'
# Linux, su disco (~/.git-credentials, in chiaro: solo se il disco è cifrato):
git config --global credential.helper store

git clone https://<host>/alice/app.git      # utente: alice, password: gst_...
```

Senza helper, il token si può mettere nell'URL (`https://alice:gst_...@<host>/alice/app.git`),
ma finisce in `.git/config` e nella history della shell: meglio un helper.
Per gli script, `GIT_ASKPASS` o `git -c http.extraHeader=...` non servono: basta
l'helper `store`.

## Instradamento

Le richieste git non passano dal gateway: un `IngressRoute` Traefik
(`deploy/gitstack/templates/ingress-git.yaml`) manda
`/<owner>/<repo>.git/{info/refs,git-upload-pack,git-receive-pack}` al servizio
git, che autentica da sé con identity (`/internal/verify`,
`/internal/permissions/check`) e risolve owner/nome tramite core
(`GET /repos/{owner}/{repo}` con l'identità firmata dell'utente, che applica
la lettura). Il gateway risponde con errori JSON e sessioni, non con il
`401 WWW-Authenticate: Basic` che git richiede, e un push grosso non deve
attraversare il suo proxy. Il controllo dei permessi è nel pacchetto
`services/git/internal/access`, riusabile dal server SSH (GIT-71).

Fuori da questo item: regole di ricezione (limite 100 MB per file, protezione
del branch principale, repo archiviati: GIT-72) ed evento `git.push`.
