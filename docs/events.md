# Eventi su NATS JetStream

Convenzioni per pubblicare e consumare eventi tra i servizi di GitStack sul
bus NATS JetStream (decisione D11), usando la libreria condivisa
`pkg/events` del workspace Go (`go.work`). Architettura di riferimento:
`.lmbrain-lite/knowledge/architecture.md`.

## Envelope

Ogni evento viaggia in una busta comune (`events.Envelope`), non nel solo
payload applicativo:

```json
{
  "name": "core.resource.test.created",
  "version": 1,
  "id": "b3c1a6b0-...-uuid",
  "time": "2026-09-28T10:22:38Z",
  "payload": { "resourceId": "res-1", "type": "test", "name": "demo" }
}
```

- **name**: nome dell'evento, dot-separated, vedi sotto. È anche il subject NATS su cui viene pubblicato.
- **version**: intero, versione dello schema del *payload* per quel nome. Parte da 1.
- **id**: uuid v4, generato alla pubblicazione (`events.NewEnvelope`), identifica il singolo evento (non la risorsa a cui si riferisce).
- **time**: timestamp UTC di creazione dell'evento, non di consegna.
- **payload**: JSON applicativo specifico dell'evento, tipizzato lato Go da un pacchetto come `pkg/events/testevent`.

## Nomi degli eventi (= subject NATS)

Formato: `<dominio>.<risorsa>.<azione>` (o `<dominio>.<azione>` per eventi
più semplici), minuscolo, segmenti separati da punto, nessun carattere jolly
nel nome stesso (i jolly `*` e `>` sono per chi si iscrive, non per chi
pubblica). Il primo segmento è il **dominio**: di norma il servizio che
pubblica l'evento.

Esempi già previsti dall'architettura:

| Nome evento | Dominio | Pubblicato da | Significato |
|---|---|---|---|
| `git.push` | `git` | servizio `git` | dopo ogni push accettato, HTTPS o SSH (schema v1 sotto) |
| `issue.created`, `issue.closed`, ... | `issue` | servizio `core` | ciclo di vita di una issue |
| `issue_comment.created`, ... | `issue_comment` | servizio `core` | commenti delle issue (M-06, sotto) |
| `repository.created`, ... | `repository` | servizio `core` | ciclo di vita di un repo (M-06, sotto; sostituisce il segnaposto `repo.*`) |
| `core.resource.test.created` | `core` | servizio `core` | evento di prova (vedi sotto) |

`events.Domain(name)` estrae il dominio da un nome evento.

## Nomi degli stream

Uno stream JetStream per dominio, che raccoglie tutti i subject di quel
dominio con un wildcard: subject `"<dominio>.>"`. Il nome dello stream è il
dominio in SCREAMING_SNAKE_CASE (qui coincide con il maiuscolo semplice
perché i domini sono parole singole): `git` → `GIT`, `issue` → `ISSUE`,
`repository` → `REPOSITORY`, `issue_comment` → `ISSUE_COMMENT`, `core` → `CORE`.

Solo il servizio che possiede il dominio pubblica sul suo stream (stesso
principio di "uno schema Postgres per servizio": nessun servizio scrive
sullo stream di un altro). `events.EnsureStream(ctx, js, domain)` crea o
aggiorna lo stream con questa convenzione; va chiamato dal servizio
pubblicante prima di `Publisher.Publish` (JetStream rifiuta la
pubblicazione su un subject non coperto da nessuno stream).

## Nomi dei consumer durevoli

Un consumer durevole (`Durable` in `events.EnsureDurableConsumer`) mantiene
il proprio avanzamento (ack) anche tra un riavvio e l'altro del servizio
consumatore: il nome durevole è la sua identità persistente e non va
cambiato a cuor leggero (un nome nuovo riparte da zero). Convenzione:
`<servizio-consumatore>-<scopo>`, minuscolo con trattini, es.
`core-issue-linker` per il consumer di core che collega commit e issue dai
`git.push`.

## Versioni di schema e compatibilità

Il payload di un evento può cambiare forma nel tempo. Quando cambia in modo
incompatibile, la versione sale (es. da 1 a 2); il nome dell'evento resta lo
stesso. Un consumer registra solo le coppie (nome, versione) che sa
decodificare (`events.Registry.Register`); alla ricezione di una versione
non registrata, `Registry.Decode` ritorna un `*events.UnknownSchemaError`
invece di far fallire in modo brusco la decodifica: il consumer decide come
trattarla (di norma: loggare e terminare il messaggio con `msg.Term()`,
senza riconsegna, senza far crashare il processo). Vedi il test
`TestConsumer_UnknownVersionHandledWithoutCrash` in `pkg/events`.

## Publish con conferma

`Publisher.Publish` (in `pkg/events`) pubblica sul subject pari al nome
dell'evento e ritorna solo dopo l'ack sincrono di JetStream (il server ha
scritto il messaggio sullo stream); l'errore di ritorno è non-nil se e solo
se JetStream non ha confermato la scrittura. Non usa la pubblicazione
asincrona: per il volume di eventi previsto in v1 la semplicità conta più
del throughput.

## Evento di prova

`pkg/events/testevent` definisce l'evento di prova usato per validare la
libreria stessa (test d'integrazione in `pkg/events`, NATS JetStream reale)
e, in GIT-5, pubblicato dal servizio core alla creazione della risorsa di
prova:

- nome: `core.resource.test.created`
- versione: 1
- payload: `{ "resourceId": string, "type": string, "name": string }`

## `git.push` (schema v1)

Pubblicato dal servizio `git` (dominio `git`, stream `GIT`, subject
`git.push`) dopo ogni push **accettato**, via HTTPS o SSH. Lo schema è in
`pkg/events/gitpush` (`Name`, `Version`, `Payload`, `Decode`, `Register`):
un consumer registra `gitpush.Register(reg)` e decodifica con
`Registry.Decode`. Consumatori previsti (regole M-06): core chiude le issues
con `fixes #n` solo quando il commit entra nel branch principale (C2) e solo
se chi ha fatto il push ha `write` sul repo della issue (C1); il webhook push
(C6) ricalca quello di GitHub (`ref`, `before`, `after`, `commits`,
`repository`, `sender`).

Un evento per push, non per ref: un `git push` di più branch e tag produce
un solo evento con più elementi in `refs`.

### Campi del payload

| Campo | Significato |
|---|---|
| `repo.id` | id del repo (uuid, lo stesso di core e identity) |
| `repo.fullName` | `owner/repo` |
| `repo.defaultBranch` | branch principale al momento del push (nome corto, es. `main`; R4) |
| `pusher.id`, `pusher.username` | l'utente **autenticato** che ha fatto il push (token o chiave SSH), non l'autore dei commit |
| `pusher.type` | `human` o `agent` (se identity non lo dice, `human`) |
| `refs[].ref` | nome completo del ref: `refs/heads/<branch>`, `refs/tags/<tag>` |
| `refs[].before` | sha prima del push; `0000000000000000000000000000000000000000` se il ref è stato **creato** |
| `refs[].after` | sha dopo il push; `0000…0000` se il ref è stato **eliminato** |
| `refs[].forced` | `true` se `before` non è antenato di `after` (force-push, anche di un tag spostato); sempre `false` per creazione ed eliminazione |
| `refs[].isDefaultBranch` | `true` se il ref è `refs/heads/<repo.defaultBranch>`; mai per i tag |
| `refs[].commits` | commit nuovi raggiungibili da `after`, i più recenti per primi, al massimo **100** (`gitpush.MaxCommits`); `[]` (mai `null`) per un ref eliminato o senza commit nuovi |
| `refs[].commitsTruncated` | `true` se i commit nuovi sono più di 100 |
| `commits[].sha` | sha del commit |
| `commits[].author`, `commits[].committer` | `{ "name", "email", "date" }`, `date` in RFC 3339 |
| `commits[].message` | messaggio **completo** (oggetto, corpo, a capo finale come in git) |

Tag e branch diversi dal principale hanno la stessa forma del branch
principale. Per un tag annotato `after` è lo sha dell'oggetto tag, non del
commit puntato.

### Quali commit sono «nuovi»

- Ref **aggiornato**: i commit raggiungibili da `after` e non da `before`,
  cioè `git log before..after`.
- Ref **creato**: i commit raggiungibili da `after` e non da nessun ref
  (branch o tag) che esisteva prima del push. In un repo vuoto, tutta la
  storia.
- Ref **eliminato**: nessuno.

### Elenco troncato: come ricostruire il resto

Oltre 100 commit nuovi l'elenco contiene i 100 più recenti
(`commitsTruncated: true`). Chi ha bisogno di tutti i commit li ricostruisce
dal repo, col servizio `git` (letture commit/diff) o con git:

```sh
git log before..after          # ref aggiornato (anche force-push)
git log after --not <altri ref> # ref creato: before è lo sha zero
```

Per un force-push `before..after` dà i commit della nuova storia; quelli
della vecchia storia, `after..before`, sono i commit scartati. Il confronto
vale finché git non fa il garbage collect degli oggetti non più raggiungibili.

### Esempio completo

Push di `main` (con `fixes #12`) e del tag `v1.0.0` fatto da un utente agent:

```json
{
  "name": "git.push",
  "version": 1,
  "id": "0b6f3a52-7d1c-4c0e-9a39-2d1f5c8e7a41",
  "time": "2026-10-05T12:30:00Z",
  "payload": {
    "repo": {
      "id": "3f1d2c4e-8a55-4b6e-9d0a-1c2b3d4e5f60",
      "fullName": "ada/demo",
      "defaultBranch": "main"
    },
    "pusher": {
      "id": "7a9e1b20-5c3d-4e8f-a1b2-c3d4e5f60718",
      "username": "build-bot",
      "type": "agent"
    },
    "refs": [
      {
        "ref": "refs/heads/main",
        "before": "1111111111111111111111111111111111111111",
        "after": "2222222222222222222222222222222222222222",
        "forced": false,
        "isDefaultBranch": true,
        "commits": [
          {
            "sha": "2222222222222222222222222222222222222222",
            "author": { "name": "Ada Lovelace", "email": "ada@example.com", "date": "2026-10-05T12:20:00Z" },
            "committer": { "name": "Ada Lovelace", "email": "ada@example.com", "date": "2026-10-05T12:20:00Z" },
            "message": "Corregge il parser\n\nfixes #12\n"
          }
        ],
        "commitsTruncated": false
      },
      {
        "ref": "refs/tags/v1.0.0",
        "before": "0000000000000000000000000000000000000000",
        "after": "3333333333333333333333333333333333333333",
        "forced": false,
        "isDefaultBranch": false,
        "commits": [],
        "commitsTruncated": false
      }
    ]
  }
}
```

### Se NATS non risponde

Il push **non si perde mai per colpa di NATS**: il servizio git risponde al
client appena `git receive-pack` è finito, e l'evento si pubblica dopo, in
background.

- All'avvio la connessione a NATS (`GITSTACK_GIT_NATS_URL`) non è
  obbligatoria: si riprova ogni 2 secondi e lo stream `GIT` si crea alla
  prima pubblicazione riuscita. Senza `GITSTACK_GIT_NATS_URL` il servizio
  parte e lo dice nel log (`git.push non verrà pubblicato`).
- Ogni pubblicazione ha 3 tentativi da 5 secondi, con attesa di 1 e poi 2
  secondi fra l'uno e l'altro (ogni tentativo aspetta l'ack di JetStream).
- Se anche l'ultimo fallisce l'evento **è perso**: il servizio registra
  l'errore nel log (`git.push: pubblicazione non riuscita, evento perso`,
  con repo, numero di tentativi e numero di ref) e il push resta accettato.
  Non c'è una coda su disco: un consumer che non può perdere un push deve
  poterlo ricostruire dal repo (`git for-each-ref`) con una riconciliazione
  periodica. Se il servizio si ferma con pubblicazioni in corso, aspetta fino
  a 15 secondi prima di uscire.
- Se la lettura dei ref prima o dopo il push fallisce (disco), l'evento non
  si pubblica e l'errore va nel log; i commit non leggibili di un ref danno
  `commits: []` ma il ref resta nell'evento.

Limite noto: i ref prima e dopo il push si leggono con `git for-each-ref`
attorno al `receive-pack`. Due push concorrenti sullo stesso repo possono
vedersi a vicenda; in quel caso un evento può contenere ref aggiornati
dall'altro push, e nessun ref va perso.

## Eventi di dominio di core (M-06): `issue.*`, `issue_comment.*`, `repository.*`

Contratto fissato da GIT-129; li pubblica il servizio `core` **dopo** aver
confermato la transazione che cambia lo stato (mai prima: un evento senza
modifica sarebbe peggio di una modifica senza evento), e li consumano in core
le notifiche (M-06/B, consumer `core-notifier`), i webhook (`core-webhooks`)
e il collegamento commit↔issue (`core-issue-linker`, su `git.push`).
Sostituiscono il segnaposto `repo.*` della tabella sopra: il dominio del
repo si chiama `repository`, come l'evento webhook.

Tre domini, tre stream (`ISSUE`, `ISSUE_COMMENT`, `REPOSITORY`; subject
`issue.>`, `issue_comment.>`, `repository.>`). `issue_comment` è un dominio a
sé, e non `issue.comment.*`, perché i subject `issue.>` e `issue_comment.>`
non si sovrappongono e uno stream per dominio resta la regola. Tutti a
versione **1**. Gli schemi (nomi, tipi dei payload, decoder) sono in
`pkg/events/coreevents`: un consumer chiama `coreevents.Register(reg)`.

#### Pubblicazione (outbox transazionale, GIT-130)

A differenza di `git.push`, gli eventi di core **non si perdono** e **non
escono mai per una modifica fallita**: core usa un'outbox transazionale.

- La modifica scrive l'evento nella tabella `core.event_outbox` **nella stessa
  transazione Postgres** (migrazione 0009). Se la transazione fallisce o
  fa rollback (anche dopo aver già accodato l'evento) non resta niente, né
  nell'outbox né sullo stream; se arriva al commit l'evento esiste e prima o
  poi parte, anche con NATS fermo o dopo un riavvio di core.
- Un relay in background (`internal/outbox`, avviato da `core serve`) legge
  le righe non inviate (`FOR UPDATE SKIP LOCKED`: sicuro con più repliche),
  le pubblica con `Publish` con ack di JetStream e solo dopo segna
  `sent_at`. Se NATS non risponde o rifiuta, la riga resta pendente con
  `attempts` e `last_error` aggiornati e il tentativo successivo è spostato
  in avanti di 2 s, poi 4, 8… fino a 5 minuti (attesa crescente, senza limite
  di tentativi). Il relay parte subito all'avvio (riprende quel che era
  rimasto) e poi controlla ogni secondo. La connessione NATS si riapre da sola.
- L'**id della busta è fissato nell'outbox** alla scrittura (`event_outbox.id`
  = `envelope.id` = `Nats-Msg-Id`) e resta uguale a ogni tentativo: se il
  relay muore fra l'ack e l'aggiornamento della riga l'evento si ripubblica,
  e i consumatori (idempotenti sull'`id`; JetStream deduplica comunque nella
  sua finestra) lo scartano. Consegna **at-least-once**.
- `envelope.time` è il momento della modifica (la riga dell'outbox), non
  quello dell'invio. L'ordine di invio è quello di scrittura, ma non è
  garantito fra un tentativo e l'altro: i payload sono istantanee dello stato
  dopo la modifica, quindi un consumer legge lo stato attuale dal database
  quando l'ordine conta.
- `actor.type` e `assignee.type` (`human`/`agent`) li risolve il relay
  chiedendo a identity al momento dell'invio (la richiesta non lo sa): se
  identity non risponde l'invio si ritenta, un utente sconosciuto è `human`.
- Le righe inviate si tengono 7 giorni (debug) e poi il relay le toglie.
- `mentions[]` non è ancora valorizzato (assente = nessuna menzione): core non
  ha ancora un parser delle menzioni; lo aggiungerà il lavoro sulle notifiche.
  `issue.referenced` e `issue.commit_linked` li pubblica il collegamento
  commit↔issue (`core-issue-linker`), non questa parte.
- «Via token» non fa parte del contratto: lo porterà GIT-125.

Pubblicati da core: `issue.created` (porta già gli assegnatari iniziali, poi
un `labeled`/`assigned`/`milestoned` per ciascuno), `edited` (uno solo anche se
cambiano titolo e testo), `closed`, `reopened`, `assigned`, `unassigned`,
`labeled`, `unlabeled`, `milestoned`, `demilestoned`, `locked`, `unlocked`,
`hidden`, `unhidden`; `issue_comment.created|edited|deleted`; `repository.created`,
`archived`, `unarchived`, `deleted`, `restored`, `visibility_changed`. Eliminare
un'etichetta o una milestone emette un `unlabeled`/`demilestoned` per ogni issue
toccata. I consumatori sono idempotenti sull'`id` della busta
(`envelope.id`): JetStream può consegnare due volte.

### Campi comuni del payload

| Campo | Significato |
|---|---|
| `repo` | `{ id, fullName, defaultBranch, visibility, archived }` come in core al momento dell'evento |
| `actor` | `{ id, username, type }` chi ha agito (`type`: `human` o `agent`); `null` per eventi di sistema (es. chiusura da commit) |

I payload sono **istantanee minime**: gli id e i campi che servono a decidere
chi notificare e a scrivere il testo. Chi deve il dettaglio completo (i
webhook costruiscono il corpo di `docs/webhooks.md`) lo legge dal database di
core, che è lo stesso servizio. Gli id sono uuid, `number` è il `#n` della
issue.

### `issue.*`

Campo `issue` in tutti: `{ id, number, title, state, authorId, assigneeIds,
hidden, locked }` (stato **dopo** il cambiamento). Le issues nascoste
pubblicano comunque l'evento (per la cronologia), con `hidden: true`: notifiche
e webhook li scartano.

| Evento | Campi in più | Note |
|---|---|---|
| `issue.created` | `mentions[]` (user id menzionati nel titolo/testo) | apertura (I1) |
| `issue.edited` | `changes`: `{ title?: { from }, body?: { from } }`, `mentions[]` (nuovi) | |
| `issue.closed` | `reason` (`completed`, `not_planned`, `duplicate`), `duplicateOf?`, `commit?`: `{ sha, repository }` | con `commit` la chiusura viene da `fixes #n` (C2) e `actor` è `null` |
| `issue.reopened` | | |
| `issue.assigned`, `issue.unassigned` | `assignee`: `{ id, username, type }` | un evento per assegnatario |
| `issue.labeled`, `issue.unlabeled` | `label`: `{ id, name, color }` | |
| `issue.milestoned`, `issue.demilestoned` | `milestone`: `{ id, number, title }` | |
| `issue.locked`, `issue.unlocked` | `reason?` | |
| `issue.hidden`, `issue.unhidden` | | |
| `issue.referenced` | `source`: `{ kind, repository, number, commentId? }` (C1) | un'altra issue/PR cita questa; nasce da `core.issue_references` e dalla riga «referenced from» della cronologia |
| `issue.commit_linked` | `commit`: `{ sha, repository, subject, ref, closeKeyword? }` (C2) | un commit cita la issue, su qualunque branch; riga «linked commit» |

Con `fixes #n` su un branch non principale c'è solo `issue.commit_linked`; la
chiusura (`issue.closed` con `commit`, e riga «closed by commit <sha>» della
cronologia, tipo `closed_by_commit`) arriva quando il commit entra nel
branch principale, e solo se chi ha fatto il push ha `write` sul repo della
issue (C1, C2). `issue.referenced` e `issue.commit_linked` non cambiano
`issue.updatedAt`.

### `issue_comment.*`

Campi: `issue` come sopra e `comment`: `{ id, authorId, body }` (per
`deleted` senza `body`, I4), `mentions[]` (utenti menzionati nel testo nuovo)
e, per `edited`, `changes`: `{ body: { from } }`.

| Evento | Quando |
|---|---|
| `issue_comment.created` | nuovo commento |
| `issue_comment.edited` | modifica del testo da parte dell'autore |
| `issue_comment.deleted` | eliminazione (resta la traccia «comment deleted», I4) |

### `repository.*`

Campi: solo i comuni, più `changes` dove indicato.

| Evento | Campi in più |
|---|---|
| `repository.created` | |
| `repository.deleted` | `deletedAt`, `purgeAt` (7 giorni dopo, R2) |
| `repository.restored` | |
| `repository.archived`, `repository.unarchived` | |
| `repository.visibility_changed` | `changes`: `{ visibility: { from } }` |

La rinomina e il trasferimento non esistono nella v1 (R3): niente eventi.

### Corrispondenza con i webhook e le notifiche

| Evento di dominio | Webhook | Notifica (motivo) |
|---|---|---|
| `git.push` | `push`, uno per ref | commit citati: `commit_linked` agli iscritti |
| `issue.created` | `issues` / `opened` | `mentioned`, `assigned`, `subscribed` (Watch `all`) |
| `issue.edited` | `issues` / `edited` | `mentioned` (nuovi menzionati) |
| `issue.closed`, `issue.reopened` | `issues` / `closed`, `reopened` | `state_change` |
| `issue.assigned`, `issue.unassigned` | `issues` / `assigned`, `unassigned` | `assigned` all'assegnatario |
| `issue.labeled` … `issue.unlocked` | `issues` / azione omonima | nessuna |
| `issue.hidden`, `issue.unhidden` | nessuno | nessuna |
| `issue.referenced` | nessuno | nessuna |
| `issue.commit_linked` | nessuno | `commit_linked` agli iscritti |
| `issue_comment.created` | `issue_comment` / `created` | `mentioned`, `participating`, `subscribed` |
| `issue_comment.edited` | `issue_comment` / `edited` | `mentioned` (nuovi menzionati) |
| `issue_comment.deleted` | `issue_comment` / `deleted` | nessuna |
| `repository.*` | `repository` / azione omonima | nessuna |

Chi riceve una notifica lo decide il consumer: iscritti alla issue
(`core.issue_subscriptions`), watch del repo (`core.repo_watches`), menzioni e
assegnatari; mai chi ha causato l'evento, anche via token (C3). Un repo in
Watch `ignore` non notifica, salvo le menzioni dirette. La notifica
`webhook` (C7) non nasce da un evento NATS: la scrive il job di consegna
quando disattiva un webhook.

Consumer durevoli (convenzione `<servizio>-<scopo>`; un consumer JetStream
appartiene a uno stream, quindi si aggiunge il dominio): `core-notifier-issue`,
`core-notifier-issue_comment` (notifiche), `core-webhooks-git`,
`core-webhooks-issue`, `core-webhooks-issue_comment`,
`core-webhooks-repository` (webhook) e `core-issue-linker` (su `git.push`).
Nomi stabili: cambiarli riparte da zero.

## Come si avvia NATS per i test

Il test d'integrazione di `pkg/events` (tag di build `integration`, vedi
`pkg/events/integration_test.go`) avvia un `nats-server` v2 reale
**in-process**, con JetStream abilitato e storage su una cartella
temporanea (`github.com/nats-io/nats-server/v2/server`, stesso motore e
stesso protocollo di un nats-server standalone): non serve Docker né una
rete esterna, funziona identico in locale e in CI con

```sh
go test -tags=integration ./...
```

eseguito dentro `pkg/events`. Il test parla solo con l'interfaccia
`jetstream.JetStream` di `nats.go`: puntarlo a un NATS esterno vero (un
container, o un servizio dedicato in CI) richiederebbe di sostituire solo la
funzione che apre la connessione, non il resto del test.
