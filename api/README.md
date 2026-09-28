# api/

Fonte unica dell'API pubblica di GitStack (decisione D8 [c_f68d7615da93197a]):
il documento `openapi.yaml` (OpenAPI 3.0.3) è il contratto da cui si generano
i client, non un'estrazione a posteriori dal codice.

- `openapi.yaml`: il contratto. Versione del contratto in `info.version`
  (SemVer); versione dell'API esplicita nel percorso (`servers: /v1`).
- `redocly.yaml`: configurazione del linter ([Redocly CLI](https://redocly.com/docs/cli/)),
  estende il ruleset `recommended`.

## Principi del modello

- **Risorse generiche** (decisione D15 [c_4ef209e79c5b2ddb]): un solo tipo
  `Resource` con un campo `type` aperto (stringa, non enum), non un tipo per
  ogni concetto di prodotto. Oggi la sola risorsa di prova dello scheletro
  end-to-end; domani repository, applicazioni, database, senza cambiare
  forma dell'API.
- **Errori in un formato unico**: ogni risposta di errore usa lo schema
  `Error` (`{ error: { code, message, details? } }`).
- **API-first** (decisione D7 [c_3780bc749c6ed28d]): nessun endpoint o campo
  esclusivo della web UI; UI, CLI `gs`, skills e in futuro server MCP usano
  tutti lo stesso contratto.

## Verificare il contratto

```sh
npx --yes @redocly/cli@2 lint api/openapi.yaml --config api/redocly.yaml
```

Girato anche da `./scripts/generate-api.sh` (radice del monorepo) e dal
check CI `api-contract` (`.github/workflows/api-contract.yml`).

## Cambiare il contratto

1. Modifica `openapi.yaml` (e questo README se cambiano i principi).
2. Dalla radice: `go work sync && ./scripts/generate-api.sh` per rigenerare
   `client/go` e `client/ts`.
3. Commit del contratto insieme al codice generato: il check CI fallisce se
   non sono allineati.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice); i
client generati in `client/` sono invece Apache-2.0 (decisione D17
[c_1d1d61aca3dea601]).
