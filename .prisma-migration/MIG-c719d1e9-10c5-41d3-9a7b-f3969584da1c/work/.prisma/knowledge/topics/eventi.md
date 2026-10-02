---
{"area":"technical-choices","id":"DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/architecture.md","source_id":"SRC-5d952707-85b7-46bf-bf7c-41dc9a714236"}],"tags":["eventi","nats"],"title":"Eventi su NATS JetStream","updated":"2026-09-30T21:13:45.439291800+00:00"}
---

# Eventi su NATS JetStream

## Context

Convenzioni del bus eventi (D11), realizzate in `pkg/events`. Fonte nel repo: `docs/events.md`.

## Confirmed decisions

- **Envelope comune:** `name`, `version` (versione dello schema del payload, parte da 1), `id` (uuid dell'evento), `time` (UTC di creazione), `payload`.
- **Nomi** `<dominio>.<risorsa>.<azione>` minuscoli; il nome è anche il subject NATS; il dominio è il servizio che pubblica. Previsti: `git.push` (git), `issue.*` e `repo.*` (core); oggi esiste l'evento di prova `core.resource.test.created` v1.
- **Uno stream per dominio** (`GIT`, `ISSUE`, `REPO`, `CORE`); solo il servizio proprietario pubblica sul suo stream.
- **Consumer durevoli** chiamati `<servizio>-<scopo>` (es. `core-issue-linker`); il nome non si cambia a cuor leggero.
- **Compatibilità:** un cambio incompatibile alza la versione; una versione sconosciuta viene scartata senza far cadere il consumer.
- **Publish con conferma sincrona** di JetStream: semplicità prima del throughput in v1.

## Related topics

- [[knowledge/topics/architettura]]

