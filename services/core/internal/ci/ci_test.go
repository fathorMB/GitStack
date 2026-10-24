package ci

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, src string) *Workflow {
	t.Helper()
	wf, err := ParseWorkflow(".github/workflows/ci.yml", []byte(src))
	if err != nil {
		t.Fatalf("ParseWorkflow: %v", err)
	}
	return wf
}

const jobs = "\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"

func TestParseWorkflow_FormeDiOn(t *testing.T) {
	for name, src := range map[string]string{
		"scalare":   "on: push" + jobs,
		"sequenza":  "on: [push, pull_request]" + jobs,
		"mappa":     "on:\n  push:\n  pull_request:\n" + jobs,
		"mappa-bis": "name: X\non:\n  push:\n    branches: [main]\n" + jobs,
	} {
		wf := mustParse(t, src)
		if wf.Push == nil {
			t.Errorf("%s: push non letto", name)
		}
	}
	if wf := mustParse(t, "on: [push, pull_request]"+jobs); wf.PullRequest == nil {
		t.Error("pull_request non letto dalla sequenza")
	}
}

func TestParseWorkflow_Errori(t *testing.T) {
	for name, src := range map[string]string{
		"senza on":      "name: x" + jobs,
		"senza jobs":    "on: push\n",
		"yaml rotto":    "on: [push\n",
		"needs ignoto":  "on: push\njobs:\n  a:\n    needs: zz\n    runs-on: x\n",
		"non una mappa": "- a\n- b\n",
	} {
		if _, err := ParseWorkflow("w.yml", []byte(src)); err == nil {
			t.Errorf("%s: atteso un errore", name)
		}
	}
}

func TestParseWorkflow_NomeEJobInOrdine(t *testing.T) {
	wf := mustParse(t, "name: Build\non: push\njobs:\n  zeta:\n    name: Ultimo\n    runs-on: ubuntu-latest\n  alfa:\n    needs: [zeta]\n    runs-on: [self-hosted, linux]\n")
	if wf.Name != "Build" {
		t.Errorf("nome %q", wf.Name)
	}
	if len(wf.Jobs) != 2 || wf.Jobs[0].Key != "zeta" || wf.Jobs[0].Name != "Ultimo" || wf.Jobs[1].Key != "alfa" {
		t.Fatalf("job fuori ordine: %+v", wf.Jobs)
	}
	if runnerTypeOf(wf.Jobs[0].RunsOn) != "internal_pod" || runnerTypeOf(wf.Jobs[1].RunsOn) != "external" {
		t.Error("runner type sbagliato")
	}
}

func TestRunnerTypeOf_EtichetteENormalizzazione(t *testing.T) {
	for runsOn, want := range map[string]string{
		"ubuntu-latest":    "internal_pod",
		"Ubuntu-24.04":     "internal_pod",
		"self-hosted":      "external",
		"windows-latest":   "external",
		"macos-14":         "external",
		"gpu":              "external",
		"linux:host":       "external",
		"${{ matrix.os }}": "internal_pod",
	} {
		if got := runnerTypeOf([]string{runsOn}); got != want {
			t.Errorf("%q: %s, atteso %s", runsOn, got, want)
		}
	}
	if got := NormalizeLabels([]string{" Linux:host ", "linux", "", "GPU"}); len(got) != 2 || got[0] != "linux" || got[1] != "gpu" {
		t.Errorf("NormalizeLabels = %v", got)
	}
}

func push(ref string, changed ...string) Event {
	return Event{Kind: EventPush, Ref: ref, SHA: strings.Repeat("a", 40), Changed: changed}
}

func TestPush_Branch(t *testing.T) {
	wf := mustParse(t, "on:\n  push:\n    branches: [main, 'release/**', '!release/old']"+jobs)
	for ref, want := range map[string]bool{
		"refs/heads/main":          true,
		"refs/heads/release/1.0":   true,
		"refs/heads/release/a/b":   true,
		"refs/heads/release/old":   false,
		"refs/heads/feature":       false,
		"refs/tags/main":           false, // filtro sui soli branch: un tag non parte
		"refs/tags/v1":             false,
		"refs/heads/main-extra":    false,
		"refs/heads/xmain":         false,
		"refs/heads/release":       false,
		"refs/heads/release/a/old": true,
	} {
		if got := wf.Matches(push(ref)); got != want {
			t.Errorf("%s: %v, atteso %v", ref, got, want)
		}
	}
}

func TestPush_BranchesIgnore(t *testing.T) {
	wf := mustParse(t, "on:\n  push:\n    branches-ignore: ['wip/**']"+jobs)
	if !wf.Matches(push("refs/heads/main")) || wf.Matches(push("refs/heads/wip/x")) {
		t.Error("branches-ignore non applicato")
	}
	// Senza filtri sui tag, un tag parte con branches-ignore soltanto? No: solo i branch.
	if wf.Matches(push("refs/tags/v1")) {
		t.Error("con soli filtri sui branch un tag non deve partire")
	}
}

func TestPush_Tag(t *testing.T) {
	wf := mustParse(t, "on:\n  push:\n    tags: ['v*', '!v0.*']"+jobs)
	for ref, want := range map[string]bool{
		"refs/tags/v1":      true,
		"refs/tags/v1.2.3":  true,
		"refs/tags/v0.9":    false,
		"refs/tags/release": false,
		"refs/heads/v1":     false,
	} {
		if got := wf.Matches(push(ref)); got != want {
			t.Errorf("%s: %v, atteso %v", ref, got, want)
		}
	}
	both := mustParse(t, "on:\n  push:\n    branches: [main]\n    tags: ['v*']"+jobs)
	if !both.Matches(push("refs/heads/main")) || !both.Matches(push("refs/tags/v2")) || both.Matches(push("refs/heads/dev")) {
		t.Error("branches e tags insieme")
	}
}

func TestPush_SenzaFiltriVaBeneOgniRef(t *testing.T) {
	wf := mustParse(t, "on: push"+jobs)
	for _, ref := range []string{"refs/heads/qualsiasi", "refs/tags/v1"} {
		if !wf.Matches(push(ref)) {
			t.Errorf("%s dovrebbe partire", ref)
		}
	}
	if wf.Matches(push("refs/notes/x")) {
		t.Error("un ref che non è branch né tag non parte")
	}
}

func TestPush_Eliminazione(t *testing.T) {
	wf := mustParse(t, "on: push"+jobs)
	ev := push("refs/heads/main")
	ev.Deleted = true
	if wf.Matches(ev) {
		t.Error("l'eliminazione di un branch non avvia niente")
	}
}

func TestPush_Paths(t *testing.T) {
	wf := mustParse(t, "on:\n  push:\n    paths: ['services/**', '!services/docs/**', '*.md']"+jobs)
	cases := []struct {
		changed []string
		want    bool
	}{
		{[]string{"services/core/main.go"}, true},
		{[]string{"README.md"}, true},
		{[]string{"docs/README.md"}, false}, // * non attraversa «/»
		{[]string{"services/docs/a.md"}, false},
		{[]string{"web/x.ts", "services/core/a.go"}, true},
		{[]string{"web/x.ts"}, false},
		{nil, true}, // file sconosciuti: i filtri non escludono
	}
	for _, c := range cases {
		if got := wf.Matches(push("refs/heads/main", c.changed...)); got != c.want {
			t.Errorf("%v: %v, atteso %v", c.changed, got, c.want)
		}
	}
	// Un push che non cambia file (changed vuoto ma noto) non soddisfa `paths`.
	if wf.Matches(Event{Kind: EventPush, Ref: "refs/heads/main", Changed: []string{}}) {
		t.Error("nessun file cambiato: paths non soddisfatto")
	}
	// Un workflow con soli paths non parte su un tag.
	if wf.Matches(push("refs/tags/v1", "services/a.go")) {
		t.Error("solo paths: un tag non parte")
	}
	// I tag non guardano i paths.
	tags := mustParse(t, "on:\n  push:\n    tags: ['v*']\n    paths: ['x/**']"+jobs)
	if !tags.Matches(push("refs/tags/v1", "altro.txt")) {
		t.Error("paths non si valuta sui tag")
	}
}

func TestPush_PathsIgnore(t *testing.T) {
	wf := mustParse(t, "on:\n  push:\n    paths-ignore: ['docs/**', '**.md']"+jobs)
	cases := []struct {
		changed []string
		want    bool
	}{
		{[]string{"docs/a.txt"}, false},
		{[]string{"docs/a.txt", "README.md"}, false}, // tutti ignorati
		{[]string{"docs/a.txt", "main.go"}, true},
		{[]string{"src/x/y.md"}, false},
	}
	for _, c := range cases {
		if got := wf.Matches(push("refs/heads/main", c.changed...)); got != c.want {
			t.Errorf("%v: %v, atteso %v", c.changed, got, c.want)
		}
	}
}

func pull(action, base string, changed ...string) Event {
	return Event{Kind: EventPull, Ref: "refs/heads/feat", PullNumber: 1, PullAction: action, BaseBranch: base, Changed: changed}
}

func TestPull_TipiDiDefault(t *testing.T) {
	wf := mustParse(t, "on: pull_request"+jobs)
	for action, want := range map[string]bool{"opened": true, "synchronize": true, "reopened": true, "closed": false, "labeled": false} {
		if got := wf.Matches(pull(action, "main")); got != want {
			t.Errorf("%s: %v, atteso %v", action, got, want)
		}
	}
	custom := mustParse(t, "on:\n  pull_request:\n    types: [closed]"+jobs)
	if !custom.Matches(pull("closed", "main")) || custom.Matches(pull("opened", "main")) {
		t.Error("types esplicito")
	}
}

func TestPull_BranchDiDestinazioneEPaths(t *testing.T) {
	wf := mustParse(t, "on:\n  pull_request:\n    branches: [main]\n    paths: ['api/**']"+jobs)
	if !wf.Matches(pull("opened", "main", "api/openapi.yaml")) {
		t.Error("PR verso main con file in api/ deve partire")
	}
	if wf.Matches(pull("opened", "develop", "api/openapi.yaml")) {
		t.Error("destinazione diversa")
	}
	if wf.Matches(pull("opened", "main", "web/a.ts")) {
		t.Error("paths non soddisfatto")
	}
	ig := mustParse(t, "on:\n  pull_request:\n    branches-ignore: [wip]"+jobs)
	if ig.Matches(pull("opened", "wip")) || !ig.Matches(pull("opened", "main")) {
		t.Error("branches-ignore sulla destinazione")
	}
}

func TestPull_UnPushNonEUnaPull(t *testing.T) {
	wf := mustParse(t, "on: pull_request"+jobs)
	if wf.Matches(push("refs/heads/main")) {
		t.Error("un workflow solo pull_request non parte su push")
	}
	onlyPush := mustParse(t, "on: push"+jobs)
	if onlyPush.Matches(pull("opened", "main")) {
		t.Error("un workflow solo push non parte su pull_request")
	}
}

func TestDispatch(t *testing.T) {
	wf := mustParse(t, "on:\n  workflow_dispatch:\n    inputs:\n      env:\n        required: true\n      level:\n        default: info\n"+jobs)
	if !wf.Dispatch || !wf.Matches(Event{Kind: EventDispatch}) {
		t.Fatal("workflow_dispatch non letto")
	}
	if wf.Matches(push("refs/heads/main")) {
		t.Error("solo dispatch: il push non basta")
	}
	if len(wf.DispatchInputs) != 2 || !wf.DispatchInputs[0].Required || wf.DispatchInputs[1].Default != "info" {
		t.Fatalf("inputs: %+v", wf.DispatchInputs)
	}
	if _, err := resolveInputs(wf.DispatchInputs, nil); err == nil {
		t.Error("manca l'input obbligatorio")
	}
	if _, err := resolveInputs(wf.DispatchInputs, map[string]string{"env": "x", "zzz": "y"}); err == nil {
		t.Error("input non dichiarato")
	}
	got, err := resolveInputs(wf.DispatchInputs, map[string]string{"env": "prod"})
	if err != nil || got["env"] != "prod" || got["level"] != "info" {
		t.Errorf("default non applicato: %v %v", got, err)
	}
	if mustParse(t, "on: push"+jobs).Dispatch {
		t.Error("senza workflow_dispatch non si avvia a mano")
	}
}

func TestSkipCI(t *testing.T) {
	for msg, want := range map[string]bool{
		"fix: x [skip ci]":                  true,
		"fix: x\n\n[ci skip]":               true,
		"[no ci] docs":                      true,
		"chore [SKIP CI]":                   true,
		"x [skip actions]":                  true,
		"x [actions skip]":                  true,
		"x\n\nskip-checks: true":            true,
		"fix: skip ci senza parentesi":      false,
		"fix: [skip] ci":                    false,
		"normale":                           false,
		"":                                  false,
		"x\n\nskip-checks: false":           false,
		"parla di skip-checks: true inline": false,
	} {
		if got := SkipCI(msg); got != want {
			t.Errorf("%q: %v, atteso %v", msg, got, want)
		}
	}
}

func TestRepoState_Blocked(t *testing.T) {
	cases := []struct {
		name string
		st   RepoState
		want string
	}{
		{"normale", RepoState{}, ""},
		{"archiviato", RepoState{Archived: true}, ReasonArchived},
		{"eliminato", RepoState{Deleted: true}, ReasonDeleted},
		{"mirror in pull, spento di default", RepoState{PullMirror: true}, ReasonPullMirror},
		{"mirror in pull acceso", RepoState{PullMirror: true, RunOnPullMirror: true}, ""},
		{"archiviato anche con mirror acceso", RepoState{Archived: true, PullMirror: true, RunOnPullMirror: true}, ReasonArchived},
	}
	for _, c := range cases {
		if got := c.st.Blocked(); got != c.want {
			t.Errorf("%s: %q, atteso %q", c.name, got, c.want)
		}
	}
}

func TestRepoState_Dormant(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := RepoState{CreatedAt: now.AddDate(0, -6, 0), LastPushAt: now.Add(-DormantAfter + time.Hour)}
	if st.Dormant(now) {
		t.Error("59 giorni e 23 ore: non ancora sospeso")
	}
	st.LastPushAt = now.Add(-DormantAfter - time.Hour)
	if !st.Dormant(now) {
		t.Error("oltre 60 giorni: sospeso")
	}
	// Senza push registrati vale la creazione.
	if !(RepoState{CreatedAt: now.AddDate(0, -3, 0)}).Dormant(now) {
		t.Error("creato 3 mesi fa e mai pushato")
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"main", "main", true},
		{"main", "mainx", false},
		{"*", "a", true},
		{"*", "a/b", false},
		{"**", "a/b/c", true},
		{"feature/*", "feature/x", true},
		{"feature/*", "feature/x/y", false},
		{"feature/**", "feature/x/y", true},
		{"**/*.go", "a.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "a/b/c.js", false},
		{"v?.0", "v1.0", true},
		{"v?.0", "v10.0", false},
		{"a.b", "axb", false}, // il punto è letterale
		{"è*", "èa", true},
	}
	for _, c := range cases {
		if got := Glob(c.pat, c.name); got != c.want {
			t.Errorf("Glob(%q, %q) = %v, atteso %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestIsWorkflowPath(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yml":        true,
		".gitstack/workflows/ci.yaml":     true,
		".github/workflows/sub/ci.yml":    false,
		".github/workflows/readme.md":     false,
		"workflows/ci.yml":                false,
		".github/actions/x/action.yml":    false,
		".gitstack/workflows/.keep":       false,
		".gitstack/workflows/a.yml.bak":   false,
		".github/workflowsx/ci.yml":       false,
		"x/.github/workflows/ci.yml":      false,
		".gitstack/workflows/release.yml": true,
	} {
		if got := IsWorkflowPath(p); got != want {
			t.Errorf("%s: %v, atteso %v", p, got, want)
		}
	}
}

func TestParseAll_PrecedenzaDiGitstack(t *testing.T) {
	a := []byte("name: Gitstack\non: push" + jobs)
	b := []byte("name: Github\non: push" + jobs)
	only := []byte("name: Solo github\non: push" + jobs)
	wfs := parseAll([]File{
		{Path: ".github/workflows/ci.yml", Content: b},
		{Path: ".gitstack/workflows/ci.yml", Content: a},
		{Path: ".github/workflows/altro.yml", Content: only},
		{Path: ".github/workflows/rotto.yml", Content: []byte("on: [")},
	}, nil2())
	got := map[string]string{}
	for _, w := range wfs {
		got[w.Path] = w.Name
	}
	if len(got) != 2 || got[".gitstack/workflows/ci.yml"] != "Gitstack" || got[".github/workflows/altro.yml"] != "Solo github" {
		t.Fatalf("workflow letti: %v", got)
	}
}
