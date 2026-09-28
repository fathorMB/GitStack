# pkg/

Librerie Go condivise tra i servizi di GitStack, ciascuna un modulo a sé
(vedi `go.work` in radice), non un servizio a sé stante.

| Pacchetto | Ruolo |
|-----------|-------|
| `events/` | Publisher/consumer per il bus NATS JetStream, con envelope di evento versionato (nome, versione, id, timestamp, payload JSON). Convenzioni di nomi in `docs/events.md`. |

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).
