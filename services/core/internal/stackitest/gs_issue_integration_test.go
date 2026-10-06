//go:build integration

package stackitest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// M-07/E (GIT-166): `gs issue` sullo stack completo. Il binario gs è quello
// vero (compilato con go build da cli/cmd/gs) e parla col gateway vero
// attraverso un piccolo proxy che mappa /api/v1 su /v1 (in produzione lo fa
// il web). Utenti e repo sono quelli di newIssuesE2E: alice (admin), bob
// (write), carol (read, autrice di #1), botty (agente con write), dave.

type gsRun struct {
	out, errOut string
	code        int
}

type gsEnv struct {
	*issuesE2E
	bin   string
	proxy string
}

func newGsEnv(t *testing.T) *gsEnv {
	t.Helper()
	e := newIssuesE2E(t)
	bin := build(t, "../../../../cli/cmd/gs", "gs")
	gw, err := url.Parse(e.gateway)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(gw)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, "/api/v1")
		r.Host = gw.Host
	}
	srv := httptest.NewServer(rp)
	t.Cleanup(srv.Close)
	return &gsEnv{issuesE2E: e, bin: bin, proxy: srv.URL}
}

// gs lancia il binario come `user` (token read+write) sul repo alice/<repo>.
// user "" = token inventato.
func (g *gsEnv) gs(user, repo, stdin string, args ...string) gsRun {
	g.t.Helper()
	token := "gst_inventato0000000000000000000000000000"
	if user != "" {
		token = g.tokens[user]
	}
	return g.gsToken(token, repo, stdin, args...)
}

func (g *gsEnv) gsToken(token, repo, stdin string, args ...string) gsRun {
	g.t.Helper()
	full := append([]string{"--hostname", g.proxy}, args...)
	if repo != "" {
		full = append([]string{"-R", "alice/" + repo}, full...)
	}
	cmd := exec.Command(g.bin, full...)
	cmd.Dir = g.t.TempDir() // fuori da un repo: niente remote origin
	cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+g.t.TempDir(), "GS_TOKEN="+token, "HOME="+g.home, "USERPROFILE="+g.home)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			g.t.Fatalf("gs %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return gsRun{out.String(), errOut.String(), code}
}

func (g *gsEnv) ok(user, repo, stdin string, args ...string) gsRun {
	g.t.Helper()
	r := g.gs(user, repo, stdin, args...)
	if r.code != 0 {
		g.t.Fatalf("gs %v come %s: exit %d: %s", args, user, r.code, r.errOut)
	}
	return r
}

func (g *gsEnv) fails(code int, user, repo string, args ...string) gsRun {
	g.t.Helper()
	r := g.gs(user, repo, "", args...)
	if r.code != code {
		g.t.Errorf("gs %v come %s: exit %d, voluto %d (%s)", args, user, r.code, code, r.errOut)
	}
	return r
}

// gsNumbers estrae `number` dall'output di `--json number`.
func gsNumbers(t *testing.T, out string) []int {
	t.Helper()
	var items []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("JSON non valido: %v: %s", err, out)
	}
	ns := []int{}
	for _, i := range items {
		ns = append(ns, i.Number)
	}
	sort.Ints(ns)
	return ns
}

func sorted(ns []int) []int {
	out := append([]int{}, ns...)
	sort.Ints(out)
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// viewFields sono i campi documentati di `gs issue view --json` (cli/README.md).
var viewFields = []string{"id", "number", "title", "body", "state", "closeReason", "duplicateOf", "author", "viaToken", "labels", "assignees", "milestone", "locked", "hidden", "edited", "commentCount", "attachments", "closedAt", "createdAt", "updatedAt", "url", "comments"}

func TestGsIssue(t *testing.T) {
	g := newGsEnv(t)
	I := e2eInternal
	P := e2ePrivate
	api := func(user, path string) map[string]any {
		return g.must(user, "GET", "/repos/alice/"+I+path, nil, 200).json()
	}

	t.Run("preparazione", func(t *testing.T) {
		work := g.initWork(g.httpsURL("alice", g.tokens["alice"], "/alice/"+I+".git"), map[string]string{
			"README.md":                       "# prova\n",
			".gitstack/ISSUE_TEMPLATE/bug.md": "---\ntitle: Bug Report\nabout: Segnala un bug\nlabels:\n  - bug\n---\n\n## Passi per riprodurre\n",
		})
		g.mustGit(work, "", "push", "-q", "origin", "main")
		g.must("bob", "POST", "/repos/alice/"+I+"/labels", map[string]any{"name": "area-rete", "color": "1d76db"}, 201)
		g.must("bob", "POST", "/repos/alice/"+I+"/milestones", map[string]any{"title": "v1"}, 201)
	})

	t.Run("create", func(t *testing.T) {
		// I3: chi ha solo read apre; il testo arriva da stdin
		r := g.ok("carol", I, "stack trace\nsu due righe\n", "issue", "create", "-t", "Crash all'avvio", "-F", "-")
		if got := strings.TrimSpace(r.out); got != g.proxy+"/alice/"+I+"/issues/1" {
			t.Errorf("indirizzo: %q", got)
		}
		if b, _ := api("carol", "/issues/1")["body"].(string); b != "stack trace\nsu due righe\n" {
			t.Errorf("testo da stdin: %q", b)
		}
		// senza write niente etichette: exit 5, nessuna issue in più
		g.fails(5, "carol", I, "issue", "create", "-t", "Con etichetta", "-l", "bug")
		if api("carol", "/issues")["total"] != float64(1) {
			t.Errorf("create negata ha creato una issue")
		}
		// uso errato: senza titolo, modello inesistente (l'errore elenca i modelli)
		g.fails(2, "bob", I, "issue", "create")
		if r := g.fails(2, "bob", I, "issue", "create", "-t", "x", "-T", "nuovo"); !strings.Contains(r.errOut, "bug") {
			t.Errorf("l'errore non elenca i modelli: %s", r.errOut)
		}
		// modello + etichette + assegnatario agente + milestone per titolo
		r = g.ok("bob", I, "", "issue", "create", "-T", "bug", "-l", "area-rete", "-a", "botty", "-m", "v1", "--json", "number,title,labels,assignees,milestone,body,url")
		var is struct {
			Number    int    `json:"number"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			URL       string `json:"url"`
			Labels    []struct{ Name string }
			Assignees []struct{ Username string }
			Milestone struct{ Title string }
		}
		if err := json.Unmarshal([]byte(r.out), &is); err != nil {
			t.Fatal(err, r.out)
		}
		names := []string{}
		for _, l := range is.Labels {
			names = append(names, l.Name)
		}
		sort.Strings(names)
		if is.Number != 2 || is.Title != "Bug Report" || !strings.Contains(is.Body, "Passi per riprodurre") ||
			strings.Join(names, ",") != "area-rete,bug" || len(is.Assignees) != 1 || is.Assignees[0].Username != "botty" || is.Milestone.Title != "v1" ||
			!strings.HasSuffix(is.URL, "/alice/"+I+"/issues/2") {
			t.Errorf("create da modello: %+v", is)
		}
		// altre issue per i test seguenti
		g.ok("bob", I, "", "issue", "create", "-t", "Da completare", "-b", "x")
		g.ok("bob", I, "", "issue", "create", "-t", "Doppione del crash", "-b", "x", "-a", "@me")
		g.ok("bob", I, "", "issue", "create", "-t", "Fuori programma", "-b", "x")
		if api("carol", "/issues")["total"] != float64(5) {
			t.Errorf("numerazione: %v", api("carol", "/issues"))
		}
	})

	t.Run("commenti_e_view", func(t *testing.T) {
		g.ok("bob", I, "", "issue", "comment", "1", "-b", "riproduco, **grazie**")
		r := g.ok("carol", I, "mi succede sempre\n", "issue", "comment", "#1", "-F", "-", "--json", "id,body,url")
		_ = r
		g.fails(2, "carol", I, "issue", "comment", "1")
		v := g.ok("carol", I, "", "issue", "view", "1", "--comments")
		for _, want := range []string{"#1 Crash all'avvio", "Stato: aperta", "stack trace", "bob", "riproduco, **grazie**", "Commenti: 2"} {
			if !strings.Contains(v.out, want) {
				t.Errorf("view senza %q:\n%s", want, v.out)
			}
		}
		if strings.Contains(g.ok("carol", I, "", "issue", "view", "1").out, "riproduco") {
			t.Errorf("i commenti compaiono senza --comments")
		}
		j := g.ok("carol", I, "", "issue", "view", "1", "--json", "number,commentCount,comments")
		var got struct {
			Number       int
			CommentCount int
			Comments     []struct{ Body string }
		}
		if err := json.Unmarshal([]byte(j.out), &got); err != nil || got.Number != 1 || got.CommentCount != 2 || len(got.Comments) != 2 {
			t.Errorf("view --json: %v %s", err, j.out)
		}
		if q := g.ok("carol", I, "", "issue", "view", "1", "--jq", ".state"); strings.TrimSpace(q.out) != "open" {
			t.Errorf("--jq: %q", q.out)
		}
		g.fails(6, "carol", I, "issue", "view", "999")
		g.fails(2, "carol", I, "issue", "view", "abc")
	})

	t.Run("modifica", func(t *testing.T) {
		g.ok("carol", I, "", "issue", "edit", "1", "-t", "Crash all'avvio (v1)", "-b", "nuovo testo")
		if is := api("carol", "/issues/1"); is["title"] != "Crash all'avvio (v1)" || is["body"] != "nuovo testo" || is["edited"] != true {
			t.Errorf("edit dell'autore: %v", is)
		}
		g.fails(5, "bob", I, "issue", "edit", "1", "-t", "altrui") // solo l'autore (I4)
		g.fails(5, "carol", I, "issue", "edit", "1", "--add-label", "bug")
		g.ok("bob", I, "", "issue", "edit", "1", "--add-label", "bug", "--add-label", "area-rete", "--add-assignee", "bob", "-m", "v1")
		g.ok("bob", I, "", "issue", "edit", "1", "--remove-label", "area-rete", "--add-assignee", "botty", "--remove-assignee", "bob")
		is := api("carol", "/issues/1")
		var ls, as []string
		for _, l := range is["labels"].([]any) {
			ls = append(ls, l.(map[string]any)["name"].(string))
		}
		for _, a := range is["assignees"].([]any) {
			as = append(as, a.(map[string]any)["username"].(string))
		}
		if strings.Join(ls, ",") != "bug" || strings.Join(as, ",") != "botty" || is["milestone"].(map[string]any)["title"] != "v1" {
			t.Errorf("etichette/assegnatari/milestone: %v", is)
		}
		g.ok("bob", I, "", "issue", "edit", "1", "--remove-milestone")
		if api("carol", "/issues/1")["milestone"] != nil {
			t.Errorf("milestone non tolta")
		}
		g.fails(2, "bob", I, "issue", "edit", "1")
	})

	t.Run("list_coerente_con_API", func(t *testing.T) {
		g.ok("bob", I, "", "issue", "close", "5", "-r", "not planned")
		g.ok("bob", I, "", "issue", "close", "4", "-r", "duplicate #1")
		cases := []struct {
			name string
			user string
			args []string
			api  string
		}{
			{"predefinito_aperte", "carol", nil, ""},
			{"state_all", "carol", []string{"--state", "all"}, "state=all"},
			{"state_closed", "carol", []string{"-s", "closed"}, "state=closed"},
			{"label", "carol", []string{"-l", "bug", "-s", "all"}, "labels=bug&state=all"},
			{"assegnatario", "carol", []string{"-a", "botty"}, "assignee=botty"},
			{"assegnatario_me", "bob", []string{"-a", "@me", "-s", "all"}, "assignee=@me&state=all"},
			{"autore", "carol", []string{"-A", "carol", "-s", "all"}, "author=carol&state=all"},
			{"autore_me", "bob", []string{"-A", "@me", "-s", "all"}, "author=@me&state=all"},
			{"milestone", "carol", []string{"-m", "v1", "-s", "all"}, "milestone=v1&state=all"},
			{"senza_assegnatario", "carol", []string{"-a", "none"}, "assignee=none"},
			{"search_stato_e_motivo", "carol", []string{"-S", "is:closed reason:duplicate"}, "q=" + url.QueryEscape("is:closed reason:duplicate")},
			{"search_negazione", "carol", []string{"-S", "-label:bug is:open"}, "q=" + url.QueryEscape("-label:bug is:open")},
			{"search_no", "carol", []string{"-S", "no:assignee is:open"}, "q=" + url.QueryEscape("no:assignee is:open")},
			{"search_testo", "carol", []string{"-S", "crash", "-s", "all"}, "q=crash&state=all"},
			{"search_e_filtri", "carol", []string{"-S", "crash", "-l", "bug", "-s", "all"}, "q=crash&labels=bug&state=all"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				r := g.ok(c.user, I, "", append([]string{"issue", "list", "--json", "number"}, c.args...)...)
				got := gsNumbers(t, r.out)
				want := sorted(numbers(g.must(c.user, "GET", "/repos/alice/"+I+"/issues?"+c.api, nil, 200)))
				if !sameInts(got, want) {
					t.Errorf("gs %v = %v, API ?%s = %v", c.args, got, c.api, want)
				}
				if len(want) == 0 && c.name != "senza_assegnatario" && c.name != "search_no" && c.name != "search_negazione" {
					t.Errorf("caso senza risultati: non prova niente")
				}
			})
		}
		// la sintassi non valida è un 422 dell'API (exit 1), non un crash
		if r := g.fails(1, "carol", I, "issue", "list", "-S", "is:boh"); !strings.Contains(r.errOut, "gs:") {
			t.Errorf("errore: %q", r.errOut)
		}
		// tabella senza TTY: una riga per record, colonne separate da tab
		r := g.ok("carol", I, "", "issue", "list", "-s", "all", "-S", "reason:duplicate")
		cols := strings.Split(strings.TrimSpace(r.out), "\t")
		if len(cols) != 5 || cols[0] != "#4" || cols[1] != "closed:duplicate" || cols[4] != "Doppione del crash" {
			t.Errorf("tabella: %q", r.out)
		}
		g.fails(2, "carol", I, "issue", "list", "-s", "boh")
		g.fails(2, "carol", I, "issue", "list", "-L", "0")
		// -L limita
		if n := gsNumbers(t, g.ok("carol", I, "", "issue", "list", "-s", "all", "-L", "2", "--json", "number").out); len(n) != 2 {
			t.Errorf("--limit: %v", n)
		}
	})

	t.Run("chiusura_riapertura_I2_I3", func(t *testing.T) {
		is := api("carol", "/issues/5")
		if is["state"] != "closed" || is["closeReason"] != "not_planned" {
			t.Errorf("not planned: %v", is)
		}
		is = api("carol", "/issues/4")
		if is["closeReason"] != "duplicate" || is["duplicateOf"] != float64(1) {
			t.Errorf("duplicate #n: %v", is)
		}
		g.ok("bob", I, "", "issue", "reopen", "5")
		g.ok("bob", I, "", "issue", "close", "5", "--comment", "fatto") // completed (predefinito)
		if is := api("carol", "/issues/5"); is["closeReason"] != "completed" || is["commentCount"] != float64(1) {
			t.Errorf("completed con commento: %v", is)
		}
		g.ok("bob", I, "", "issue", "reopen", "5")
		if is := api("carol", "/issues/5"); is["state"] != "open" || is["closeReason"] != nil {
			t.Errorf("riapertura azzera il motivo (I2): %v", is)
		}
		g.ok("bob", I, "", "issue", "close", "5", "-r", "duplicate", "-d", "2")
		if is := api("carol", "/issues/5"); is["closeReason"] != "duplicate" || is["duplicateOf"] != float64(2) {
			t.Errorf("--duplicate-of: %v", is)
		}
		g.fails(1, "bob", I, "issue", "close", "5")  // già chiusa: 409
		g.fails(1, "bob", I, "issue", "reopen", "3") // già aperta: 409
		g.fails(2, "bob", I, "issue", "close", "3", "-r", "duplicate")
		g.fails(1, "bob", I, "issue", "close", "3", "-r", "duplicate", "-d", "3") // duplicato di se stessa: 422
		g.fails(1, "bob", I, "issue", "close", "3", "-r", "duplicate", "-d", "99")
		// I3: l'autore chiude e riapre la propria, senza write non gestisce le altre
		g.ok("carol", I, "", "issue", "close", "1", "-r", "not planned")
		if api("carol", "/issues/1")["state"] != "closed" {
			t.Errorf("l'autrice non ha chiuso la sua")
		}
		g.ok("carol", I, "", "issue", "reopen", "1")
		if api("carol", "/issues/1")["state"] != "open" {
			t.Errorf("l'autrice non ha riaperto la sua")
		}
		g.fails(5, "carol", I, "issue", "close", "2")
		g.fails(5, "carol", I, "issue", "reopen", "4")
		if api("carol", "/issues/2")["state"] != "open" || api("carol", "/issues/4")["state"] != "closed" {
			t.Errorf("senza write ha cambiato stato")
		}
	})

	t.Run("lock_unlock_I11", func(t *testing.T) {
		g.fails(5, "bob", I, "issue", "lock", "1") // serve admin
		g.ok("alice", I, "", "issue", "lock", "1", "-r", "fuori tema")
		g.ok("alice", I, "", "issue", "lock", "1") // idempotente
		if api("carol", "/issues/1")["locked"] != true {
			t.Errorf("non bloccata")
		}
		g.fails(5, "carol", I, "issue", "comment", "1", "-b", "ancora io")
		g.ok("bob", I, "", "issue", "comment", "1", "-b", "chi ha write commenta")
		g.fails(5, "bob", I, "issue", "unlock", "1")
		j := g.ok("alice", I, "", "issue", "unlock", "1", "--json", "locked")
		if strings.TrimSpace(j.out) != `{"locked":false}` {
			t.Errorf("unlock: %q", j.out)
		}
		g.ok("alice", I, "", "issue", "unlock", "1") // idempotente
		g.ok("carol", I, "", "issue", "comment", "1", "-b", "di nuovo")
	})

	t.Run("repo_archiviato_R10", func(t *testing.T) {
		g.patchRepo("alice", I, map[string]any{"archived": true})
		defer g.patchRepo("alice", I, map[string]any{"archived": false})
		for _, args := range [][]string{
			{"issue", "create", "-t", "x"},
			{"issue", "comment", "1", "-b", "x"},
			{"issue", "close", "3"},
			{"issue", "edit", "1", "-b", "x"},
		} {
			user := "bob"
			if args[1] == "create" {
				user = "carol"
			}
			r := g.fails(1, user, I, args...)
			if !strings.Contains(r.errOut, "archiviato") || !strings.Contains(r.errOut, "alice/"+I) {
				t.Errorf("%v: messaggio non chiaro: %q", args, r.errOut)
			}
			if r.out != "" {
				t.Errorf("%v: stdout non vuoto: %q", args, r.out)
			}
		}
		// in sola lettura funzionano
		g.ok("carol", I, "", "issue", "list")
		g.ok("carol", I, "", "issue", "view", "1")
		// con --json l'errore è nel formato dell'API
		r := g.fails(1, "bob", I, "issue", "comment", "1", "-b", "x", "--json", "id")
		if !strings.Contains(r.errOut, `"code":"archived"`) {
			t.Errorf("errore JSON: %q", r.errOut)
		}
	})

	t.Run("errori_4_5_6", func(t *testing.T) {
		// 4: token non valido; nessun token in locale
		g.fails(4, "", I, "issue", "list")
		g.fails(4, "", I, "issue", "create", "-t", "x")
		if r := g.gsToken("", I, "", "issue", "list"); r.code != 4 {
			t.Errorf("senza token: exit %d (%s)", r.code, r.errOut)
		}
		// 5: permesso negato
		g.fails(5, "carol", I, "issue", "edit", "2", "-t", "x")
		// 6: non trovato, e il repo privato non si rivela (404 e non 403)
		priv := g.open("bob", P, privTitle, privBody, nil)
		for _, args := range [][]string{
			{"issue", "view", "1"}, {"issue", "list"}, {"issue", "comment", "1", "-b", "x"},
			{"issue", "close", "1"}, {"issue", "create", "-t", "x"},
		} {
			r := g.fails(6, "carol", P, args...)
			g.issueLeak("gs "+strings.Join(args, " "), r.out+r.errOut)
		}
		_ = priv
		g.fails(6, "carol", "inesistente", "issue", "view", "1")
		g.fails(6, "bob", I, "issue", "close", "999")
		g.fails(6, "bob", I, "issue", "create", "-t", "x", "-m", "milestone-che-non-c-e")
		// l'errore JSON su stderr, stdout vuoto
		r := g.fails(6, "carol", I, "issue", "view", "999", "--json", "title")
		if r.out != "" || !strings.Contains(r.errOut, `"error"`) {
			t.Errorf("errore JSON: %+v", r)
		}
	})

	t.Run("json_campi_documentati", func(t *testing.T) {
		for _, cmd := range [][]string{{"create"}, {"list"}, {"view"}, {"edit"}, {"close"}, {"reopen"}, {"lock"}, {"unlock"}, {"comment"}} {
			r := g.ok("carol", I, "", append(append([]string{"issue"}, cmd...), "--json")...)
			if !strings.Contains(r.out, "Campi disponibili") || !strings.Contains(r.out, "  number") && cmd[0] != "comment" {
				t.Errorf("%v --json: %q", cmd, r.out)
			}
		}
		// ogni campo documentato di Issue esiste davvero nella risposta (o è null se facoltativo)
		r := g.ok("carol", I, "", "issue", "view", "2", "--json", strings.Join(viewFields, ","))
		var m map[string]any
		if err := json.Unmarshal([]byte(r.out), &m); err != nil || len(m) != len(viewFields) {
			t.Errorf("campi: %v %s", err, r.out)
		}
	})
}
