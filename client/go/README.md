# client/go

Client Go generato dal contratto OpenAPI unico di GitStack
(`../../api/openapi.yaml`) con [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen)
(v2, dichiarato come tool dependency in `go.mod`, versione pinnata).

- `gitstack.gen.go`: **generato**, non modificarlo a mano — intestazione
  `Code generated ... DO NOT EDIT.` Contiene i modelli e il client HTTP per
  tutte le operazioni del contratto (`GetHealth`, `ListResources`,
  `CreateResource`, `GetResource`, `UpdateResource`, `DeleteResource`).
- `generate.go`: la direttiva `//go:generate` che invoca `oapi-codegen`.
- `oapi-codegen.yaml`: configurazione del generatore (pacchetto, modelli,
  client).

## Rigenerare

Dalla radice del monorepo (comando unico, rigenera anche il client TS):

```sh
go work sync
./scripts/generate-api.sh
```

Oppure solo questo modulo:

```sh
cd client/go
go generate ./...
```

## Uso

```go
import gitstack "github.com/fathorMB/GitStack/client/go"

c, err := gitstack.NewClientWithResponses("https://example.invalid/v1")
```

Usato da `cli/` (CLI `gs`, M-07) e, in futuro, da altri servizi Go che
chiamano l'API pubblica del `gateway`.

## Licenza

Apache-2.0 (vedi `LICENSE` in questa cartella), diversa dal resto del server
(AGPL-3.0): decisione D17 [c_1d1d61aca3dea601].
