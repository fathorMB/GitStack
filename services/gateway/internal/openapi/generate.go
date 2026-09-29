// Package openapi contiene l'interfaccia server generata dal contratto
// OpenAPI di GitStack (api/openapi.yaml): tipi e ServerInterface con un
// metodo per ogni operationId del contratto. Non modificare a mano il file
// generato (api.gen.go): rigenerarlo con `scripts/generate-api.sh` dalla
// radice del monorepo, dopo `go work sync`.
//
// Il gateway implementa questa interfaccia (vedi package handler) invece di
// definire le proprie rotte a mano: così non può discostarsi dal contratto,
// e la CI (check `api-contract`) lo verifica rigenerando e controllando che
// il repository resti pulito.
package openapi

//go:generate go run github.com/fathorMB/GitStack/api/cmd/specdump .openapi.spec.yaml
//go:generate go tool oapi-codegen -config oapi-codegen.yaml .openapi.spec.yaml
