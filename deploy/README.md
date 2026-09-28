# deploy/

Tutto ciò che serve a far girare GitStack su k3s: manifest/Helm chart interno dei servizi (`gateway`, `identity`, `git`, `core`, `web`, `postgres`, `nats`) e lo script di installazione a comando singolo. Architettura: `.lmbrain-lite/knowledge/architecture.md`.

- `gitstack/` — chart Helm interno (M-01/T-08, questo item): installa `gateway`, `core`, `web`, `postgres` e `nats` (JetStream) con un solo `helm install`, Traefik come ingresso. Dettagli, valori di default e opzioni (mirror, Postgres esterno) in `gitstack/README.md`. `identity` e `git` non hanno ancora un'immagine (arrivano con milestone successive a M-01): non sono nel chart.
- Installer v0 (M-01 T-09): non ancora arrivato.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).
