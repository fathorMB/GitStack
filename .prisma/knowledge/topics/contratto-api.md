---
{"area":"technical-choices","id":"DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","related":["DOC-84811cef-6f8e-4028-97d6-d47381172907"],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["api","openapi","client"],"title":"Contratto API e client generati","updated":"2026-10-05T09:50:00+00:00"}
---

# Contratto API e client generati

## Context

Fonti nel repo: `api/README.md`, `client/README.md`, README principale. Decisioni D7, D8, D15, D17.

## Confirmed decisions

- `api/openapi.yaml` (OpenAPI 3.0.3) è la **fonte unica** dell'API pubblica: da lì si generano client Go (`client/go`, usato dalla CLI), client TypeScript (`client/ts`, usato dalla web UI) e le interfacce server di gateway, core e identity. Il codice generato non si modifica a mano (`scripts/generate-api.sh`). `api/` è anche un modulo Go con la spec incorporata.
- Versione del contratto in `info.version` (SemVer); versione dell'API nel percorso (`/v1`).
- **Risorse generiche (D15):** un solo tipo `Resource` con campo `type` aperto (stringa), per repo oggi e app/database domani.
- **Errori in formato unico** `{ error: { code, message, details? } }` con codici stabili (es. `unauthenticated`, `insufficient_scope` con `details.required`, `validation_failed` con `details.fields`, `password_change_required`).
- **Sicurezza dichiarata nel contratto:** ogni operazione dice se è pubblica (`security: []`), quali credenziali accetta, gli scope richiesti (`x-required-scopes`) e se è esente dal cambio password (`x-password-change-exempt`). Il gateway genera da qui le regole per rotta; una rotta senza dichiarazione risponde 404.
- Tag `internal` (`/internal/*`) solo fra servizi, mai esposto dal gateway.
- Licenza: contratto AGPL-3.0; client generati Apache-2.0 (D17).
- **Contratto della CLI (G3, 2026-10-05):** i campi `--json` di `gs` e i suoi codici di uscita (`0`, `1`, `2`, `4`, `5`, `6`) sono un contratto: nella v1 si aggiungono campi ma non si tolgono né si rinominano senza una nuova versione maggiore di `gs`. Vedi [[knowledge/topics/cli-gs-skills]].
- CI: workflow `api-contract` con due job separati: `check-generated` (codice generato allineato, lint Redocly) e `workspace-sync` (`go work sync` committato).

## Related topics

- [[knowledge/topics/identita-e-sicurezza]]
- [[knowledge/topics/architettura]]

