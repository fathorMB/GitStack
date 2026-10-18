// Package toolgen genera gli strumenti MCP di `gs mcp` dal contratto OpenAPI
// (api/openapi.yaml): ogni operazione con `x-mcp.expose: true` diventa uno
// strumento, con nome `x-mcp.name`, annotazione distruttiva `x-mcp.destructive`
// e uno schema di input fatto dai suoi parametri e dal suo corpo JSON (M2, M3).
//
// Il sorgente generato (cli/internal/mcp/tools_gen.go) si committa; un test
// rigenera e confronta, quindi un contratto cambiato senza rigenerare fa
// fallire i controlli.
package toolgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tool è uno strumento ricavato da un'operazione del contratto.
type Tool struct {
	Name        string
	Operation   string
	Method      string
	Path        string
	Description string
	Destructive bool
	PathParams  []string
	QueryParams []string // parametri di query che il contratto dichiara
	Body        bool     // l'operazione ha un corpo JSON
	Schema      string   // JSON Schema dell'input
}

var methods = []string{"get", "put", "post", "delete", "patch"}

var nameRe = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)

// maxDepth limita l'espansione dei $ref nello schema di input.
const maxDepth = 3

type doc struct {
	root map[string]any
}

// Parse legge il contratto e restituisce gli strumenti da esporre, ordinati per nome.
func Parse(spec []byte) ([]Tool, error) {
	var root map[string]any
	if err := yaml.Unmarshal(spec, &root); err != nil {
		return nil, fmt.Errorf("contratto illeggibile: %w", err)
	}
	d := &doc{root: root}
	paths, _ := root["paths"].(map[string]any)
	if len(paths) == 0 {
		return nil, fmt.Errorf("il contratto non ha paths")
	}
	var tools []Tool
	seen := map[string]string{}
	for path, item := range paths {
		pi, _ := item.(map[string]any)
		for _, m := range methods {
			op, ok := pi[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			x, ok := op["x-mcp"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s %s (%s): manca x-mcp", strings.ToUpper(m), path, id)
			}
			name, _ := x["name"].(string)
			if !nameRe.MatchString(name) {
				return nil, fmt.Errorf("%s: x-mcp.name %q non è oggetto_azione", id, name)
			}
			if prev, dup := seen[name]; dup {
				return nil, fmt.Errorf("%s: x-mcp.name %q già usato da %s", id, name, prev)
			}
			seen[name] = id
			expose, ok := x["expose"].(bool)
			if !ok {
				return nil, fmt.Errorf("%s: x-mcp.expose deve essere booleano", id)
			}
			destr, ok := x["destructive"].(bool)
			if !ok {
				return nil, fmt.Errorf("%s: x-mcp.destructive deve essere booleano", id)
			}
			if !expose {
				continue
			}
			t, err := d.tool(name, id, strings.ToUpper(m), path, destr, pi, op)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			tools = append(tools, t)
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func (d *doc) tool(name, id, method, path string, destr bool, pi, op map[string]any) (Tool, error) {
	t := Tool{Name: name, Operation: id, Method: method, Path: path, Destructive: destr}
	props := map[string]any{}
	var required []string

	var params []any
	if p, ok := pi["parameters"].([]any); ok {
		params = append(params, p...)
	}
	if p, ok := op["parameters"].([]any); ok {
		params = append(params, p...)
	}
	// Un parametro dell'operazione sostituisce quello del percorso con lo stesso nome.
	byKey := map[string]map[string]any{}
	var order []string
	for _, raw := range params {
		p := d.deref(raw)
		in, _ := p["in"].(string)
		pname, _ := p["name"].(string)
		if in != "path" && in != "query" {
			continue
		}
		k := in + ":" + pname
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = p
	}
	sort.Strings(order)
	for _, k := range order {
		p := byKey[k]
		in, _ := p["in"].(string)
		pname, _ := p["name"].(string)
		s := map[string]any{"type": "string"}
		if sc, ok := p["schema"]; ok {
			s = d.schema(sc, 0)
		}
		if desc, _ := p["description"].(string); desc != "" {
			s["description"] = oneLine(desc, 300)
		}
		if _, clash := props[pname]; clash {
			return t, fmt.Errorf("il parametro %q compare due volte", pname)
		}
		props[pname] = s
		if in == "path" {
			t.PathParams = append(t.PathParams, pname)
			required = append(required, pname)
		} else {
			t.QueryParams = append(t.QueryParams, pname)
			if r, _ := p["required"].(bool); r {
				required = append(required, pname)
			}
		}
	}
	if rb, ok := op["requestBody"]; ok {
		rbm := d.deref(rb)
		content, _ := rbm["content"].(map[string]any)
		if js, ok := content["application/json"].(map[string]any); ok {
			t.Body = true
			b := map[string]any{"type": "object"}
			if sc, ok := js["schema"]; ok {
				b = d.schema(sc, 0)
			}
			b["description"] = "Corpo JSON della richiesta."
			if _, clash := props["body"]; clash {
				return t, fmt.Errorf("un parametro si chiama «body»")
			}
			props["body"] = b
			if r, _ := rbm["required"].(bool); r {
				required = append(required, "body")
			}
		} else {
			return t, fmt.Errorf("corpo non JSON: l'operazione non si può esporre come strumento")
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return t, err
	}
	t.Schema = string(b)

	summary, _ := op["summary"].(string)
	desc, _ := op["description"].(string)
	text := strings.TrimSpace(summary)
	if desc != "" {
		text += ". " + oneLine(desc, 500)
	}
	t.Description = text
	return t, nil
}

// deref risolve un $ref di primo livello (parametri, corpi, schemi).
func (d *doc) deref(v any) map[string]any {
	m, _ := v.(map[string]any)
	for i := 0; i < 8; i++ {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		m = d.lookup(ref)
	}
	return m
}

func (d *doc) lookup(ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var cur any = d.root
	for _, part := range strings.Split(ref[2:], "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	m, _ := cur.(map[string]any)
	return m
}

// schema converte uno schema OpenAPI 3.0 in JSON Schema per l'input dello
// strumento: espande i $ref fino a maxDepth (poi lascia un oggetto aperto),
// toglie quello che non serve a un modello (esempi, estensioni, campi di sola
// lettura) e traduce nullable.
func (d *doc) schema(v any, depth int) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if ref, ok := m["$ref"].(string); ok {
		if depth >= maxDepth {
			return map[string]any{"type": "object", "description": "Oggetto " + ref[strings.LastIndex(ref, "/")+1:] + " (vedi il contratto OpenAPI)."}
		}
		target := d.lookup(ref)
		if target == nil {
			return map[string]any{}
		}
		return d.schema(target, depth+1)
	}
	out := map[string]any{}
	for k, val := range m {
		switch {
		case strings.HasPrefix(k, "x-"), k == "example", k == "examples", k == "externalDocs", k == "xml",
			k == "discriminator", k == "readOnly", k == "writeOnly", k == "deprecated":
			continue
		case k == "properties":
			props := map[string]any{}
			pm, _ := val.(map[string]any)
			for pk, pv := range pm {
				if ps, ok := pv.(map[string]any); ok && d.deref(ps)["readOnly"] == true {
					continue
				}
				props[pk] = d.schema(pv, depth)
			}
			out[k] = props
		case k == "items" || k == "additionalProperties" && isMap(val) || k == "not":
			out[k] = d.schema(val, depth)
		case k == "allOf" || k == "oneOf" || k == "anyOf":
			var list []any
			for _, e := range asList(val) {
				list = append(list, d.schema(e, depth))
			}
			out[k] = list
		case k == "description":
			s, _ := val.(string)
			out[k] = oneLine(s, 300)
		case k == "required":
			if pm, ok := m["properties"].(map[string]any); ok {
				var req []string
				for _, r := range asList(val) {
					rs, _ := r.(string)
					if p, ok := pm[rs].(map[string]any); ok && d.deref(p)["readOnly"] == true {
						continue
					}
					req = append(req, rs)
				}
				sort.Strings(req)
				if len(req) > 0 {
					out[k] = req
				}
			}
		default:
			out[k] = val
		}
	}
	if n, _ := out["nullable"].(bool); n {
		delete(out, "nullable")
		if t, ok := out["type"].(string); ok {
			out["type"] = []any{t, "null"}
		}
	} else {
		delete(out, "nullable")
	}
	return out
}

func isMap(v any) bool { _, ok := v.(map[string]any); return ok }

func asList(v any) []any { l, _ := v.([]any); return l }

var spaceRe = regexp.MustCompile(`\s+`)

// oneLine riduce il testo su una riga e lo accorcia a n caratteri.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Generate produce il sorgente Go (package pkg) con la tabella Tools.
func Generate(spec []byte, pkg string) ([]byte, error) {
	tools, err := Parse(spec)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("// Code generated by cli/cmd/mcpgen from api/openapi.yaml (x-mcp); DO NOT EDIT.\n")
	b.WriteString("// Rigenera con: cd cli && go generate ./internal/mcp/...\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("// Tools sono gli strumenti dedicati: le operazioni del contratto con x-mcp.expose=true.\n")
	b.WriteString("var Tools = []Tool{\n")
	for _, t := range tools {
		b.WriteString("\t{\n")
		fmt.Fprintf(&b, "\t\tName: %q,\n\t\tOperation: %q,\n\t\tMethod: %q,\n\t\tPath: %q,\n", t.Name, t.Operation, t.Method, t.Path)
		fmt.Fprintf(&b, "\t\tDescription: %q,\n\t\tDestructive: %t,\n", t.Description, t.Destructive)
		fmt.Fprintf(&b, "\t\tPathParams: %s,\n\t\tQueryParams: %s,\n\t\tBody: %t,\n", goStrings(t.PathParams), goStrings(t.QueryParams), t.Body)
		fmt.Fprintf(&b, "\t\tSchema: %q,\n", t.Schema)
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("sorgente generato non valido: %w", err)
	}
	return out, nil
}

func goStrings(l []string) string {
	if len(l) == 0 {
		return "nil"
	}
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = fmt.Sprintf("%q", s)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}
