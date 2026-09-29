// Package api espone il contratto OpenAPI di GitStack (openapi.yaml)
// incorporato nel modulo, così i generatori lo leggono attraverso il modulo
// e non con percorsi relativi fuori dal proprio.
package api

import _ "embed"

// Spec è il contenuto di openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
