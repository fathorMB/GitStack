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
| `git.push` | `git` | servizio `git` | hook post-receive dopo un push |
| `issue.created`, `issue.closed`, ... | `issue` | servizio `core` | ciclo di vita di una issue |
| `repo.created`, ... | `repo` | servizio `core` | ciclo di vita di un repo |
| `core.resource.test.created` | `core` | servizio `core` | evento di prova (vedi sotto) |

`events.Domain(name)` estrae il dominio da un nome evento.

## Nomi degli stream

Uno stream JetStream per dominio, che raccoglie tutti i subject di quel
dominio con un wildcard: subject `"<dominio>.>"`. Il nome dello stream è il
dominio in SCREAMING_SNAKE_CASE (qui coincide con il maiuscolo semplice
perché i domini sono parole singole): `git` → `GIT`, `issue` → `ISSUE`,
`repo` → `REPO`, `core` → `CORE`.

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
