package toolgen

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const specPath = "../../../../api/openapi.yaml"
const genPath = "../tools_gen.go"

func norm(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// Gli strumenti generati devono corrispondere al contratto: se openapi.yaml
// cambia (x-mcp, parametri, corpi) senza rigenerare, il test fallisce.
func TestGeneratedToolsMatchContract(t *testing.T) {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(spec, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(norm(got), norm(want)) {
		t.Fatal("cli/internal/mcp/tools_gen.go non corrisponde ad api/openapi.yaml: rigenera con `cd cli && go generate ./internal/mcp/...`")
	}
}

func TestEveryOperationHasMCPExtension(t *testing.T) {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	// Parse fallisce se anche una sola operazione non ha x-mcp valida.
	tools, err := Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) < 30 {
		t.Errorf("strumenti generati: %d, attesi almeno 30", len(tools))
	}
}

func TestParseRejectsMissingExtension(t *testing.T) {
	spec := `paths:
  /a:
    get:
      operationId: getA
      tags: [x]
      responses: {}
`
	if _, err := Parse([]byte(spec)); err == nil || !strings.Contains(err.Error(), "manca x-mcp") {
		t.Fatalf("atteso errore su x-mcp mancante, trovato %v", err)
	}
}

func TestParseBuildsSchema(t *testing.T) {
	spec := `paths:
  /r/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema: {type: string}
    post:
      operationId: makeR
      summary: Fa R
      x-mcp: {expose: true, name: r_make, destructive: false}
      parameters:
        - $ref: "#/components/parameters/Page"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/R"
components:
  parameters:
    Page:
      name: page
      in: query
      schema: {type: integer}
  schemas:
    R:
      type: object
      required: [title, id]
      properties:
        id: {type: string, readOnly: true}
        title: {type: string, nullable: true}
`
	tools, err := Parse([]byte(spec))
	if err != nil || len(tools) != 1 {
		t.Fatalf("%v %v", tools, err)
	}
	got := tools[0]
	want := `{"additionalProperties":false,"properties":{"body":{"description":"Corpo JSON della richiesta.","properties":{"title":{"type":["string","null"]}},"required":["title"],"type":"object"},"id":{"type":"string"},"page":{"type":"integer"}},"required":["body","id"],"type":"object"}`
	if got.Schema != want || got.Method != "POST" || !got.Body || len(got.PathParams) != 1 || len(got.QueryParams) != 1 {
		t.Errorf("%+v\n%s", got, got.Schema)
	}
}
