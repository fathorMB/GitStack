// Package ci è la parte di core che trasforma push, Pull Request, avvii manuali
// e orari in esecuzioni (ci_runs) e job (ci_jobs) di una coda equa (v2-a,
// GIT-276, CI3 e CI7 [c_baa86c8ddb5db70d], ADR 0001: scheduler e coda dentro
// core). Qui non c'è l'esecuzione dei job: la fanno gli item successivi.
//
// Le regole di CI3 stanno in funzioni pure (workflow.go, match.go, cron.go,
// policy.go) per poterle provare una per una; l'accesso a Postgres sta in
// store.go e il collegamento con git ed eventi in engine.go.
package ci

import (
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Cartelle da cui si leggono i workflow, in ordine di precedenza (CI1): se lo
// stesso nome di file esiste in entrambe vale .gitstack/workflows.
var WorkflowDirs = []string{".gitstack/workflows", ".github/workflows"}

// MaxWorkflowBytes è il limite di un file workflow; oltre non si legge.
const MaxWorkflowBytes = 512 * 1024

// File è un file workflow letto da git.
type File struct {
	Path    string
	Content []byte
}

// IsWorkflowPath dice se p è un workflow (un .yml o .yaml direttamente sotto
// una delle cartelle).
func IsWorkflowPath(p string) bool {
	dir, base := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	ext := path.Ext(base)
	if ext != ".yml" && ext != ".yaml" {
		return false
	}
	for _, d := range WorkflowDirs {
		if dir == d {
			return true
		}
	}
	return false
}

// Filter è la parte branches/tags/paths di un evento.
type Filter struct {
	Branches       []string
	BranchesIgnore []string
	Tags           []string
	TagsIgnore     []string
	Paths          []string
	PathsIgnore    []string
	// Types vale per pull_request.
	Types []string
}

// DispatchInput è un input dichiarato di workflow_dispatch.
type DispatchInput struct {
	Name     string
	Required bool
	Default  string
}

// Job è un job del workflow, in ordine di dichiarazione.
type Job struct {
	Key         string
	Name        string
	Needs       []string
	RunsOn      []string
	Permissions map[string]string
}

// Workflow è un file workflow letto.
type Workflow struct {
	Path string
	Name string

	Push        *Filter
	PullRequest *Filter
	// Dispatch è true se on: contiene workflow_dispatch.
	Dispatch       bool
	DispatchInputs []DispatchInput
	// Schedules sono le espressioni cron valide (almeno 15 minuti fra due scatti).
	Schedules []string
	// Problems sono gli avvisi di lettura: un cron troppo frequente, una chiave
	// di filtro sconosciuta. Il workflow resta utilizzabile per il resto.
	Problems []string

	Jobs []Job
}

// ParseWorkflow legge un file workflow. Un YAML non valido, o senza `on` o
// senza job, è un errore: il workflow non è eseguibile.
func ParseWorkflow(p string, content []byte) (*Workflow, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("YAML non valido: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("il workflow deve essere una mappa YAML")
	}
	root := doc.Content[0]
	wf := &Workflow{Path: p, Name: p}
	if n := mapGet(root, "name"); n != nil && n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) != "" {
		wf.Name = strings.TrimSpace(n.Value)
	}
	on := mapGet(root, "on")
	if on == nil {
		// YAML 1.1 legge `on` come booleano true: con yaml.v3 resta "on", ma si
		// accetta anche "true" per i file scritti da altri strumenti.
		on = mapGet(root, "true")
	}
	if on == nil {
		return nil, fmt.Errorf("manca la chiave `on`")
	}
	if err := wf.parseOn(on); err != nil {
		return nil, err
	}
	if err := wf.parseJobs(mapGet(root, "jobs"), mapGet(root, "permissions")); err != nil {
		return nil, err
	}
	return wf, nil
}

func (wf *Workflow) parseOn(on *yaml.Node) error {
	switch on.Kind {
	case yaml.ScalarNode:
		wf.addEvent(on.Value, nil)
	case yaml.SequenceNode:
		for _, n := range on.Content {
			if n.Kind != yaml.ScalarNode {
				return fmt.Errorf("`on` contiene un elemento non valido")
			}
			wf.addEvent(n.Value, nil)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			wf.addEvent(on.Content[i].Value, on.Content[i+1])
		}
	default:
		return fmt.Errorf("`on` non valido")
	}
	return nil
}

func (wf *Workflow) addEvent(name string, body *yaml.Node) {
	switch name {
	case "push":
		wf.Push = parseFilter(body)
	case "pull_request":
		wf.PullRequest = parseFilter(body)
	case "workflow_dispatch":
		wf.Dispatch = true
		wf.DispatchInputs = parseInputs(body)
	case "schedule":
		if body == nil || body.Kind != yaml.SequenceNode {
			wf.Problems = append(wf.Problems, "schedule: serve un elenco di `cron`")
			return
		}
		for _, item := range body.Content {
			c := mapGet(item, "cron")
			if c == nil || c.Kind != yaml.ScalarNode {
				wf.Problems = append(wf.Problems, "schedule: voce senza `cron`")
				continue
			}
			expr := strings.TrimSpace(c.Value)
			if err := ValidateCron(expr); err != nil {
				wf.Problems = append(wf.Problems, fmt.Sprintf("schedule %q ignorato: %v", expr, err))
				continue
			}
			wf.Schedules = append(wf.Schedules, expr)
		}
	}
	// Gli altri eventi (issues, release, ...) non fanno parte di CI3: ignorati.
}

func parseFilter(n *yaml.Node) *Filter {
	f := &Filter{}
	if n == nil || n.Kind != yaml.MappingNode {
		return f
	}
	f.Branches = stringList(mapGet(n, "branches"))
	f.BranchesIgnore = stringList(mapGet(n, "branches-ignore"))
	f.Tags = stringList(mapGet(n, "tags"))
	f.TagsIgnore = stringList(mapGet(n, "tags-ignore"))
	f.Paths = stringList(mapGet(n, "paths"))
	f.PathsIgnore = stringList(mapGet(n, "paths-ignore"))
	f.Types = stringList(mapGet(n, "types"))
	return f
}

func parseInputs(n *yaml.Node) []DispatchInput {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	in := mapGet(n, "inputs")
	if in == nil || in.Kind != yaml.MappingNode {
		return nil
	}
	var out []DispatchInput
	for i := 0; i+1 < len(in.Content); i += 2 {
		d := DispatchInput{Name: in.Content[i].Value}
		if b := mapGet(in.Content[i+1], "required"); b != nil {
			d.Required = b.Value == "true"
		}
		if v := mapGet(in.Content[i+1], "default"); v != nil {
			d.Default = v.Value
		}
		out = append(out, d)
	}
	return out
}

func (wf *Workflow) parseJobs(jobs, topPerm *yaml.Node) error {
	if jobs == nil || jobs.Kind != yaml.MappingNode || len(jobs.Content) == 0 {
		return fmt.Errorf("manca `jobs`")
	}
	top := parsePermissions(topPerm)
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		key, body := jobs.Content[i].Value, jobs.Content[i+1]
		j := Job{Key: key, Name: key, Permissions: top}
		if n := mapGet(body, "name"); n != nil && n.Kind == yaml.ScalarNode && n.Value != "" {
			j.Name = n.Value
		}
		j.Needs = stringList(mapGet(body, "needs"))
		j.RunsOn = stringList(mapGet(body, "runs-on"))
		if p := mapGet(body, "permissions"); p != nil {
			j.Permissions = parsePermissions(p)
		}
		wf.Jobs = append(wf.Jobs, j)
	}
	keys := map[string]bool{}
	for _, j := range wf.Jobs {
		keys[j.Key] = true
	}
	for _, j := range wf.Jobs {
		for _, n := range j.Needs {
			if !keys[n] {
				return fmt.Errorf("il job %q dipende da %q, che non esiste", j.Key, n)
			}
		}
	}
	return nil
}

// parsePermissions legge `permissions:` (read-all, write-all o mappa). nil
// se assente: il default (read) lo applica chi crea il job.
func parsePermissions(n *yaml.Node) map[string]string {
	if n == nil {
		return nil
	}
	out := map[string]string{}
	switch n.Kind {
	case yaml.ScalarNode:
		lvl := "none"
		switch n.Value {
		case "read-all":
			lvl = "read"
		case "write-all":
			lvl = "write"
		}
		for _, k := range []string{"contents", "packages", "actions"} {
			out[k] = lvl
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = n.Content[i+1].Value
		}
	}
	return out
}

// mapGet è il valore di key in una mappa YAML; nil se manca.
func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// stringList legge uno scalare o una sequenza di scalari.
func stringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out
	}
	return nil
}
