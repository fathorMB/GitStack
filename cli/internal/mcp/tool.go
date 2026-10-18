// Package mcp è il server MCP di `gs mcp` (v1.1, regole M1-M5): parla il
// protocollo Model Context Protocol su stdio e traduce le chiamate degli
// strumenti in richieste all'API di GitStack, con il login, il token e
// l'istanza di gs. Gli strumenti dedicati si generano dal contratto OpenAPI
// (tools_gen.go); `api_request` copre il resto entro i permessi del token.
package mcp

//go:generate go run ../../cmd/mcpgen -spec ../../../api/openapi.yaml -out tools_gen.go -pkg mcp

// Tool è uno strumento dedicato, ricavato da un'operazione del contratto.
type Tool struct {
	Name        string
	Operation   string // operationId
	Method      string
	Path        string // con i segnaposto {param}
	Description string
	Destructive bool     // annotazione MCP destructiveHint (M3)
	PathParams  []string // parametri di percorso, tutti obbligatori
	QueryParams []string
	Body        bool   // corpo JSON nell'argomento «body»
	Schema      string // JSON Schema dell'input
}

// ReadOnly dice se lo strumento è una lettura (GET).
func (t Tool) ReadOnly() bool { return t.Method == "GET" }
