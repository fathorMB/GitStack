// Package gitstack contiene il client Go generato dal contratto OpenAPI di
// GitStack (api/openapi.yaml). Non modificare a mano i file generati
// (gitstack.gen.go): rigenerarli con `scripts/generate-api.sh` dalla radice
// del monorepo, dopo `go work sync`.
//
// Licenza Apache-2.0 (vedi LICENSE in questa cartella), diversa dal resto
// del server (AGPL-3.0): decisione D17.
package gitstack

//go:generate go run github.com/fathorMB/GitStack/api/cmd/specdump .openapi.spec.yaml
//go:generate go tool oapi-codegen -config oapi-codegen.yaml .openapi.spec.yaml
