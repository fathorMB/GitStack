// Package vapcheck valuta con un vero interprete CEL (cel-go, lo stesso linguaggio dell'API server) le
// ValidatingAdmissionPolicy che il chart rende, e prova i casi positivi e negativi dell'ADR 0003 (DB4)
// e dell'ADR 0002: non basta cercare sottostringhe nel testo (GIT-565, rework 1).
//
// Il chart si rende con helm: GITSTACK_HELM_BIN, altrimenti `helm` nel PATH, altrimenti il test salta.
// In alternativa GITSTACK_VAP_RENDERED_DIR indica una cartella con i rendering già pronti
// (dbonly.yaml e dbapps.yaml), per le macchine dove helm non si lancia direttamente. La CI lo lancia da
// deploy/tests/chart_hardening_test.sh.
package vapcheck

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"gopkg.in/yaml.v3"
)

type obj = map[string]any

const (
	coreUser = "system:serviceaccount:prova-ns:gitstack-core"
	dbPolicy = "gitstack-databases-controller-scope"
	appPol   = "gitstack-apps-controller-scope"
)

// render restituisce i documenti YAML del chart per il caso name con gli argomenti di helm template.
func render(t *testing.T, name string, args ...string) []obj {
	t.Helper()
	var out []byte
	if dir := os.Getenv("GITSTACK_VAP_RENDERED_DIR"); dir != "" {
		b, err := os.ReadFile(filepath.Join(dir, name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		out = b
	} else {
		bin := os.Getenv("GITSTACK_HELM_BIN")
		if bin == "" {
			p, err := exec.LookPath("helm")
			if err != nil {
				t.Skip("helm non trovato (GITSTACK_HELM_BIN o PATH): la prova gira da chart_hardening_test.sh in CI")
			}
			bin = p
		}
		chart, err := filepath.Abs(filepath.Join("..", "..", "..", "deploy", "gitstack"))
		if err != nil {
			t.Fatal(err)
		}
		full := append([]string{"template", "gitstack", chart, "--namespace", "prova-ns"}, args...)
		var stderr bytes.Buffer
		cmd := exec.Command(bin, full...)
		cmd.Stderr = &stderr
		out, err = cmd.Output()
		if err != nil {
			t.Fatalf("helm template: %v: %s", err, stderr.String())
		}
	}
	var docs []obj
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var d obj
		if err := dec.Decode(&d); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatal(err)
		}
		if d != nil {
			docs = append(docs, d)
		}
	}
	return docs
}

func get(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

func find(t *testing.T, docs []obj, kind, name string) obj {
	t.Helper()
	for _, d := range docs {
		if d["kind"] == kind && get(d, "metadata", "name") == name {
			return d
		}
	}
	return nil
}

// policy è una VAP compilata.
type policy struct {
	name   string
	rules  []obj
	conds  []cel.Program
	valids []cel.Program
	texts  []string
}

func compile(t *testing.T, env *cel.Env, expr string) cel.Program {
	t.Helper()
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		t.Fatalf("CEL non valida: %v\n%s", iss.Err(), expr)
	}
	p, err := env.Program(ast)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func load(t *testing.T, docs []obj, name string) *policy {
	t.Helper()
	v := find(t, docs, "ValidatingAdmissionPolicy", name)
	if v == nil {
		t.Fatalf("VAP %s non renderizzata", name)
	}
	b := find(t, docs, "ValidatingAdmissionPolicyBinding", name)
	if b == nil || get(b, "spec", "policyName") != name {
		t.Fatalf("binding di %s assente", name)
	}
	if acts, _ := get(b, "spec", "validationActions").([]any); len(acts) != 1 || acts[0] != "Deny" {
		t.Fatalf("il binding di %s deve avere solo Deny: %v", name, acts)
	}
	if get(v, "spec", "failurePolicy") != "Fail" {
		t.Fatalf("%s: failurePolicy deve essere Fail", name)
	}
	env, err := cel.NewEnv(
		cel.Variable("object", cel.DynType), cel.Variable("oldObject", cel.DynType),
		cel.Variable("request", cel.DynType), cel.Variable("namespaceObject", cel.DynType),
	)
	if err != nil {
		t.Fatal(err)
	}
	p := &policy{name: name}
	for _, r := range get(v, "spec", "matchConstraints", "resourceRules").([]any) {
		p.rules = append(p.rules, r.(map[string]any))
	}
	for _, c := range get(v, "spec", "matchConditions").([]any) {
		p.conds = append(p.conds, compile(t, env, strings.TrimSpace(get(c, "expression").(string))))
	}
	for _, c := range get(v, "spec", "validations").([]any) {
		e := strings.TrimSpace(get(c, "expression").(string))
		p.valids = append(p.valids, compile(t, env, e))
		p.texts = append(p.texts, get(c, "message").(string))
	}
	return p
}

// req è una richiesta di ammissione.
type req struct {
	user, op, resource, ns string
	object, old, nsObject  obj
}

func (r req) vars() map[string]any {
	request := obj{
		"userInfo":  obj{"username": r.user},
		"operation": r.op,
		"resource":  obj{"resource": r.resource},
		"namespace": r.ns,
	}
	return map[string]any{"object": r.object, "oldObject": r.old, "request": request, "namespaceObject": r.nsObject}
}

func (p *policy) matches(r req) bool {
	ok := false
	for _, rule := range p.rules {
		has := func(key, want string) bool {
			for _, x := range rule[key].([]any) {
				if x == want {
					return true
				}
			}
			return false
		}
		if has("resources", r.resource) && has("operations", r.op) {
			ok = true
		}
	}
	if !ok {
		return false
	}
	for _, c := range p.conds {
		v, _, err := c.Eval(r.vars())
		if err != nil || v.Value() != true {
			return false
		}
	}
	return true
}

// denials: i messaggi delle validazioni non soddisfatte (un errore di valutazione vale come rifiuto,
// failurePolicy Fail). Vuoto = ammessa, oppure policy non applicabile.
func (p *policy) denials(r req) []string {
	if !p.matches(r) {
		return nil
	}
	var out []string
	for i, c := range p.valids {
		v, _, err := c.Eval(r.vars())
		switch {
		case err != nil:
			out = append(out, fmt.Sprintf("errore CEL (%v): %s", err, p.texts[i]))
		case v.Value() != true:
			out = append(out, p.texts[i])
		}
	}
	return out
}

// --- oggetti di prova ---

func ns(name string, labels obj) obj {
	m := obj{"name": name}
	if labels != nil {
		m["labels"] = labels
	}
	return obj{"metadata": m}
}

var (
	dbLabel  = obj{"gitstack.io/managed-db": "true"}
	appLabel = obj{"gitstack.io/managed-app": "true"}
)

func rb(role string, subjects []any) obj {
	o := obj{"roleRef": obj{"kind": "ClusterRole", "name": role}}
	if subjects != nil {
		o["subjects"] = subjects
	}
	return o
}

func coreSubject() []any {
	return []any{obj{"kind": "ServiceAccount", "name": "gitstack-core", "namespace": "prova-ns"}}
}

type tcase struct {
	name  string
	r     req
	allow bool
}

func run(t *testing.T, pols []*policy, cases []tcase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var all []string
			for _, p := range pols {
				for _, d := range p.denials(c.r) {
					all = append(all, p.name+": "+d)
				}
			}
			if c.allow && len(all) > 0 {
				t.Errorf("doveva essere ammessa, rifiutata: %v", all)
			}
			if !c.allow && len(all) == 0 {
				t.Errorf("doveva essere rifiutata, ammessa")
			}
		})
	}
}

func core(op, resource, namespace string) req {
	return req{user: coreUser, op: op, resource: resource, ns: namespace}
}

func withObj(r req, o, old obj) req { r.object, r.old = o, old; return r }

func dbAndAppCases() []tcase {
	dbNS := ns("gitstack-db-1", dbLabel)
	rbOK := withObj(core("CREATE", "rolebindings", "gitstack-db-1"), rb("gitstack-db-workload", coreSubject()), nil)
	rbOK.nsObject = dbNS
	return []tcase{
		// positivi: database
		{"db: CREATE namespace db con label", withObj(core("CREATE", "namespaces", ""), dbNS, nil), true},
		{"db: UPDATE namespace db con label prima e dopo", withObj(core("UPDATE", "namespaces", ""), dbNS, dbNS), true},
		{"db: DELETE namespace db con label", withObj(core("DELETE", "namespaces", ""), nil, dbNS), true},
		{"db: RoleBinding del ruolo modello nel namespace db con label", rbOK, true},
		{"db: Service ClusterIP nel namespace db", func() req {
			r := withObj(core("CREATE", "services", "gitstack-db-1"), obj{"spec": obj{"type": "ClusterIP"}}, nil)
			r.nsObject = dbNS
			return r
		}(), true},
		{"altro utente: la policy non si applica", func() req {
			r := withObj(core("UPDATE", "namespaces", ""), ns("kube-system", nil), ns("kube-system", nil))
			r.user = "system:serviceaccount:prova-ns:altro"
			return r
		}(), true},
		// negativi: namespace db
		{"db: CREATE namespace db senza label", withObj(core("CREATE", "namespaces", ""), ns("gitstack-db-1", nil), nil), false},
		{"db: CREATE namespace db con label false", withObj(core("CREATE", "namespaces", ""), ns("gitstack-db-1", obj{"gitstack.io/managed-db": "false"}), nil), false},
		{"db: UPDATE che marca un db preesistente senza label", withObj(core("UPDATE", "namespaces", ""), dbNS, ns("gitstack-db-1", nil)), false},
		{"db: UPDATE che toglie la label", withObj(core("UPDATE", "namespaces", ""), ns("gitstack-db-1", nil), dbNS), false},
		{"db: DELETE di un gitstack-db-* senza label", withObj(core("DELETE", "namespaces", ""), nil, ns("gitstack-db-1", nil)), false},
		// negativi: namespace estranei (bypass del rework)
		{"estraneo: CREATE senza label", withObj(core("CREATE", "namespaces", ""), ns("evil", nil), nil), false},
		{"estraneo: CREATE con la label dei database", withObj(core("CREATE", "namespaces", ""), ns("evil", dbLabel), nil), false},
		{"estraneo: UPDATE kube-system", withObj(core("UPDATE", "namespaces", ""), ns("kube-system", obj{"x": "y"}), ns("kube-system", nil)), false},
		{"estraneo: UPDATE kube-system con la label dei database", withObj(core("UPDATE", "namespaces", ""), ns("kube-system", dbLabel), ns("kube-system", nil)), false},
		{"estraneo: DELETE kube-system", withObj(core("DELETE", "namespaces", ""), nil, ns("kube-system", nil)), false},
		// negativi: RoleBinding
		{"RoleBinding del ruolo modello in kube-system", withObj(core("CREATE", "rolebindings", "kube-system"), rb("gitstack-db-workload", coreSubject()), nil), false},
		{"RoleBinding del ruolo modello in un namespace db senza label", func() req {
			r := withObj(core("CREATE", "rolebindings", "gitstack-db-1"), rb("gitstack-db-workload", coreSubject()), nil)
			r.nsObject = ns("gitstack-db-1", nil)
			return r
		}(), false},
		{"RoleBinding di un altro ruolo nel namespace db", func() req {
			r := withObj(core("CREATE", "rolebindings", "gitstack-db-1"), rb("cluster-admin", coreSubject()), nil)
			r.nsObject = dbNS
			return r
		}(), false},
		{"RoleBinding con un altro soggetto nel namespace db", func() req {
			r := withObj(core("CREATE", "rolebindings", "gitstack-db-1"), rb("gitstack-db-workload", []any{obj{"kind": "ServiceAccount", "name": "altro", "namespace": "prova-ns"}}), nil)
			r.nsObject = dbNS
			return r
		}(), false},
		{"Service ExternalName nel namespace db", func() req {
			r := withObj(core("CREATE", "services", "gitstack-db-1"), obj{"spec": obj{"type": "ExternalName"}}, nil)
			r.nsObject = dbNS
			return r
		}(), false},
	}
}

func TestVAP_DatabaseSenzaApp(t *testing.T) {
	docs := render(t, "dbonly", "--set", "databases.enabled=true")
	if find(t, docs, "ValidatingAdmissionPolicy", appPol) != nil {
		t.Fatal("senza apps.enabled la policy delle app non deve esserci")
	}
	db := load(t, docs, dbPolicy)
	cases := dbAndAppCases()
	cases = append(cases,
		tcase{"app spente: RoleBinding di un altro ruolo in un namespace estraneo", withObj(core("CREATE", "rolebindings", "kube-system"), rb("view", coreSubject()), nil), false},
		tcase{"app spente: namespace gitstack-app-* non ammesso", withObj(core("CREATE", "namespaces", ""), ns("gitstack-app-1", appLabel), nil), false},
	)
	run(t, []*policy{db}, cases)
}

func TestVAP_DatabaseConApp(t *testing.T) {
	docs := render(t, "dbapps", "--set", "databases.enabled=true", "--set", "apps.enabled=true", "--set", "apps.domain=apps.acme.test")
	pols := []*policy{load(t, docs, dbPolicy), load(t, docs, appPol)}
	appNS := ns("gitstack-app-1", appLabel)
	appRB := withObj(core("CREATE", "rolebindings", "gitstack-app-1"), rb("gitstack-app-workload", coreSubject()), nil)
	appRB.nsObject = appNS
	cases := dbAndAppCases()
	cases = append(cases,
		// positivi: app, ancora soggette alla loro policy
		tcase{"app: CREATE namespace app con managed-app", withObj(core("CREATE", "namespaces", ""), appNS, nil), true},
		tcase{"app: UPDATE namespace app marcato", withObj(core("UPDATE", "namespaces", ""), appNS, appNS), true},
		tcase{"app: DELETE namespace app marcato", withObj(core("DELETE", "namespaces", ""), nil, appNS), true},
		tcase{"app: RoleBinding del ruolo delle app nel namespace app", appRB, true},
		// negativi: l'app non diventa un namespace db e il db non si lega nelle app
		tcase{"app: namespace app con la label dei database", withObj(core("CREATE", "namespaces", ""), ns("gitstack-app-1", obj{"gitstack.io/managed-app": "true", "gitstack.io/managed-db": "true"}), nil), false},
		tcase{"app: UPDATE che aggiunge la label dei database a un namespace app", withObj(core("UPDATE", "namespaces", ""), ns("gitstack-app-1", obj{"gitstack.io/managed-app": "true", "gitstack.io/managed-db": "true"}), appNS), false},
		tcase{"app: ruolo dei database legato in un namespace app", func() req {
			r := withObj(core("CREATE", "rolebindings", "gitstack-app-1"), rb("gitstack-db-workload", coreSubject()), nil)
			r.nsObject = appNS
			return r
		}(), false},
		tcase{"app: ruolo delle app legato in un namespace db", func() req {
			r := withObj(core("CREATE", "rolebindings", "gitstack-db-1"), rb("gitstack-app-workload", coreSubject()), nil)
			r.nsObject = ns("gitstack-db-1", dbLabel)
			return r
		}(), false},
		tcase{"app: namespace senza nessuna label", withObj(core("CREATE", "namespaces", ""), ns("gitstack-app-2", nil), nil), false},
	)
	run(t, pols, cases)
}
