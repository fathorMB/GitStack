# client/

Client generati dal contratto OpenAPI unico di GitStack (`api/openapi.yaml`,
decisione D8 [c_f68d7615da93197a]): niente di scritto a mano, tutto
rigenerato con un comando dalla radice del monorepo:

```sh
go work sync
./scripts/generate-api.sh
```

| Cartella | Linguaggio | Consumato da |
|----------|------------|--------------|
| `go/`    | Go         | `cli/` (CLI `gs`) e, in futuro, gli altri servizi Go che chiamano l'API pubblica tra loro |
| `ts/`    | TypeScript | `web/` (SPA React) |

## Licenza

Apache-2.0 in entrambe le sottocartelle (`LICENSE` dedicato in ciascuna),
non AGPL-3.0: il client non deve porre barriere all'integrazione, mentre il
contratto OpenAPI e il codice server restano AGPL-3.0 (decisione D17
[c_1d1d61aca3dea601], stessa scelta di `cli/` e `skills/`).

## Regola

Il codice generato (`go/gitstack.gen.go`, `ts/src/generated/**`) non si
modifica a mano. Se il contratto cambia, si cambia `api/openapi.yaml` e si
rilancia `go work sync && ./scripts/generate-api.sh`, committando anche
tutti i go.mod/go.sum che `go work sync` ha cambiato; in CI il job
`check-generated` del workflow `api-contract` fallisce se il codice
generato committato non è allineato al contratto, e il job separato
`workspace-sync` fallisce se `go work sync` non è stato committato (vedi
`.github/workflows/api-contract.yml` e il README di radice).
