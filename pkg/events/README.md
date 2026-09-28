# pkg/events

Libreria Go condivisa per pubblicare e consumare eventi sul bus NATS
JetStream (decisione D11), con schema versionato (nome, versione, id,
timestamp, payload JSON). Base futura per `git.push`, `issue.*`, CI e
deploy (D15).

- Convenzioni di nomi di subject e stream, envelope, versioni di schema: [`docs/events.md`](../../docs/events.md).
- `testevent/`: definizione dell'evento di prova (`core.resource.test.created`, v1), usato per validare qui la libreria contro un NATS JetStream reale e, in GIT-5, pubblicato dal servizio core.

## Test

```sh
go test ./...                    # unitari: Envelope, Registry
go test -tags=integration ./...  # + NATS JetStream reale (server embedded in-process, vedi integration_test.go)
```

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).
