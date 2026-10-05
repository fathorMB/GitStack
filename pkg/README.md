# pkg/

Librerie Go condivise tra i servizi di GitStack, ciascuna un modulo a sé
(vedi `go.work` in radice), non un servizio a sé stante.

| Pacchetto | Ruolo |
|-----------|-------|
| `egress/` | Client HTTP con protezione SSRF (C8) per le chiamate in uscita verso indirizzi dati dagli utenti (webhook): decide sull'IP effettivo nel dialer. Modello di minaccia nel README del pacchetto. |
| `events/` | Publisher/consumer per il bus NATS JetStream, con envelope di evento versionato (nome, versione, id, timestamp, payload JSON). Convenzioni di nomi in `docs/events.md`. |
| `names/` | Validazione dei nomi: regole R11 dei repo (`ValidateRepoName`) e nomi riservati per utenti e organizzazioni (`IsReservedOwnerName`). |

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).
