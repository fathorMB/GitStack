// Package openapi contiene l'interfaccia server generata dal contratto
// OpenAPI di GitStack (api/openapi.yaml) per le operazioni di identity che
// il servizio serve oggi (tag auth e users): tipi e ServerInterface con un
// metodo per operationId. Non modificare a mano api.gen.go: si rigenera con
// `scripts/generate-api.sh` dalla radice del monorepo, dopo `go work sync`.
package openapi

//go:generate go run github.com/fathorMB/GitStack/api/cmd/specdump .openapi.spec.yaml
//go:generate go tool oapi-codegen -config oapi-codegen.yaml .openapi.spec.yaml
