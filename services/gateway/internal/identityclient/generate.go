// Package identityclient chiama identity per verificare le credenziali
// (POST /internal/verify) e tiene in cache l'esito. I tipi di richiesta e
// risposta (types.gen.go) sono generati dal contratto: non modificarli a
// mano, rigenerarli con `scripts/generate-api.sh`.
package identityclient

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../api/openapi.yaml
