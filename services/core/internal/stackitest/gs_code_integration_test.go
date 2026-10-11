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

// M-07/G (GIT-168): `gs search`, `gs blame` e `gs browse` sullo stack
// completo, col binario gs vero e la storia del repo di TestBrowserCodice:
// alice (admin), repo privato quarzo-codice e interno quarzo-aperto, carol
// senza accesso al privato, botty agente con un commit su main.go.

type gsCode struct {
	*browserEnv
	bin, proxy string
}

func newGsCode(t *testing.T) *gsCode {
	t.Helper()
	b := newBrowserEnv(t)
	bin := build(t, "../../../../cli/cmd/gs", "gs")
	gw, err := url.Parse(b.gateway)
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
	return &gsCode{browserEnv: b, bin: bin, proxy: srv.URL}
}

// gs lancia il binario come `user` sul repo alice/<repo> ("" = nessun -R).
func (g *gsCode) gs(user, repo string, args ...string) gsRun {
	g.t.Helper()
	full := append([]string{"--hostname", g.proxy}, args...)
	if repo != "" {
		full = append([]string{"-R", "alice/" + repo}, full...)
	}
	cmd := exec.Command(g.bin, full...)
	cmd.Dir = g.t.TempDir() // fuori da un repo: niente remote origin
	cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+g.t.TempDir(), "GS_TOKEN="+g.tokens[user], "HOME="+g.home, "USERPROFILE="+g.home)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			g.t.Fatalf("gs %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return gsRun{out.String(), errOut.String(), code}
}

func (g *gsCode) ok(user, repo string, args ...string) gsRun {
	g.t.Helper()
	r := g.gs(user, repo, args...)
	if r.code != 0 {
		g.t.Fatalf("gs %v come %s: exit %d: %s", args, user, r.code, r.errOut)
	}
	return r
}

func TestGsCode(t *testing.T) {
	g := newGsCode(t)
	P, O := browserPrivate, browserOpen

	t.Run("search_issues", func(t *testing.T) {
		// una issue per repo con la stessa parola rara
		g.ok("alice", P, "issue", "create", "-t", "zaffiro nel privato", "-b", "x")
		g.ok("alice", O, "issue", "create", "-t", "zaffiro nell'aperto", "-b", "x")
		g.ok("alice", O, "issue", "create", "-t", "altra cosa", "-b", "x")

		type hit struct {
			Repo   string `json:"repo"`
			Number int    `json:"number"`
			Title  string `json:"title"`
			URL    string `json:"url"`
		}
		repos := func(user string, args ...string) []string {
			t.Helper()
			r := g.ok(user, "", append([]string{"search", "issues"}, append(args, "--json", "repo,number,title,url")...)...)
			var hs []hit
			if err := json.Unmarshal([]byte(r.out), &hs); err != nil {
				t.Fatalf("JSON: %v: %s", err, r.out)
			}
			var out []string
			for _, h := range hs {
				out = append(out, h.Repo)
				if !strings.HasSuffix(h.URL, "/"+h.Repo+"/issues/"+itoa(h.Number)) {
					t.Errorf("url: %+v", h)
				}
			}
			sort.Strings(out)
			return out
		}
		// su tutta l'installazione, nei soli repo leggibili (I10)
		if got := repos("alice", "zaffiro"); strings.Join(got, ",") != "alice/"+O+",alice/"+P {
			t.Errorf("alice: %v", got)
		}
		if got := repos("carol", "zaffiro"); strings.Join(got, ",") != "alice/"+O {
			t.Errorf("carol vede il privato: %v", got)
		}
		if got := repos("carol", "repo:alice/"+P, "zaffiro"); len(got) != 0 {
			t.Errorf("carol, repo privato: %v", got)
		}
		if got := repos("alice", "repo:alice/"+O, "is:open"); len(got) != 2 {
			t.Errorf("qualificatori: %v", got)
		}
		// testo
		if r := g.ok("alice", "", "search", "issues", "is:open", "zaffiro"); !strings.Contains(r.out, "alice/"+O) || !strings.Contains(r.out, "zaffiro nell'aperto") {
			t.Errorf("tabella: %s", r.out)
		}
		// query non interpretabile (422) e uso errato
		if r := g.gs("alice", "", "search", "issues", "is:boh"); r.code == 0 {
			t.Errorf("query non valida accettata: %+v", r)
		}
		if r := g.gs("alice", "", "search", "issues"); r.code != 2 {
			t.Errorf("senza query: %+v", r)
		}
		if r := g.gs("", "", "search", "issues", "x"); r.code != 4 {
			t.Errorf("senza token: %+v", r)
		}
	})

	t.Run("search_code", func(t *testing.T) {
		r := g.ok("alice", P, "search", "code", strings.ToLower(needleUnique))
		var paths []string
		for _, l := range strings.Split(strings.TrimSpace(r.out), "\n") {
			paths = append(paths, strings.SplitN(l, ":", 2)[0])
		}
		sort.Strings(paths)
		if strings.Join(paths, ",") != "docs/guida.md,main.go" || r.errOut != "" {
			t.Errorf("risultati: %q (stderr %q)", r.out, r.errOut)
		}
		// il ref cambia lo stato cercato: al tag v0.1 main.go non ha ancora il bot
		if r := g.ok("alice", P, "search", "code", "dal bot", "--ref", "v0.1"); strings.TrimSpace(r.out) != "" {
			t.Errorf("v0.1 ha già il bot: %q", r.out)
		}
		if r := g.ok("alice", P, "search", "code", "dal bot"); !strings.Contains(r.out, "main.go:5:") {
			t.Errorf("main: %q", r.out)
		}
		// limite di 100 (B5): detto su stderr e in JSON
		many := g.ok("alice", P, "search", "code", "lorem ipsum")
		if n := len(strings.Split(strings.TrimSpace(many.out), "\n")); n != 100 || !strings.Contains(many.errOut, "limitati ai primi 100") {
			t.Errorf("limite: %d righe, stderr %q", n, many.errOut)
		}
		j := g.ok("alice", P, "search", "code", "lorem ipsum", "--json", "limitReached,timedOut,results")
		var res struct {
			LimitReached bool `json:"limitReached"`
			TimedOut     bool `json:"timedOut"`
			Results      []struct {
				Path, URL string
				Line      int
			}
		}
		if err := json.Unmarshal([]byte(j.out), &res); err != nil {
			t.Fatal(err, j.out)
		}
		if !res.LimitReached || res.TimedOut || len(res.Results) != 100 || !strings.Contains(res.Results[0].URL, "/blob/main/") {
			t.Errorf("json: limit=%v timeout=%v n=%d", res.LimitReached, res.TimedOut, len(res.Results))
		}
		// errori: testo corto (2), nessun accesso (carol sul privato: 404 → 6)
		if r := g.gs("alice", P, "search", "code", "a"); r.code != 2 {
			t.Errorf("testo corto: %+v", r)
		}
		if r := g.gs("carol", P, "search", "code", "quarzo"); r.code != 6 || strings.Contains(r.out+r.errOut, secretFile) {
			t.Errorf("carol sul privato: %+v", r)
		}
		if r := g.ok("carol", O, "search", "code", "quarzo"); !strings.Contains(r.out, "main.go:") {
			t.Errorf("carol sull'interno: %+v", r)
		}
	})

	t.Run("blame", func(t *testing.T) {
		r := g.ok("alice", P, "blame", "main.go")
		lines := strings.Split(strings.TrimRight(r.out, "\n"), "\n")
		if len(lines) != 66 {
			t.Fatalf("righe: %d", len(lines))
		}
		if !strings.HasPrefix(lines[0], g.shaFirst[:8]+" alice ") || !strings.HasSuffix(lines[0], ") package main") {
			t.Errorf("riga 1: %q", lines[0])
		}
		if !strings.HasPrefix(lines[4], g.shaBot[:8]+" botty [agent] ") || !strings.Contains(lines[4], `println("quarzo dal bot")`) {
			t.Errorf("riga 5 (agente): %q", lines[4])
		}
		// JSON: gli stessi intervalli dell'API
		j := g.ok("alice", P, "blame", "main.go", "--json", "ranges,url")
		var bl struct {
			Ranges []struct {
				StartLine, EndLine int
				Commit             bCommit
			}
			URL string
		}
		if err := json.Unmarshal([]byte(j.out), &bl); err != nil {
			t.Fatal(err, j.out)
		}
		agent, covered := false, 0
		for _, rg := range bl.Ranges {
			covered += rg.EndLine - rg.StartLine + 1
			if rg.Commit.SHA == g.shaBot && rg.Commit.Author.User != nil && rg.Commit.Author.User.Kind == "agent" {
				agent = true
			}
		}
		if !agent || covered != 66 || !strings.HasSuffix(bl.URL, "/alice/"+P+"/blame/main/main.go") {
			t.Errorf("json: agente=%v righe=%d url=%s", agent, covered, bl.URL)
		}
		// ref: al tag v0.1 la riga 5 è del primo commit
		if r := g.ok("alice", P, "blame", "main.go", "--ref", "v0.1"); strings.Contains(r.out, "[agent]") {
			t.Errorf("v0.1 non ha il bot: %s", r.out)
		}
		// B4: binari e file oltre 1 MB danno un errore chiaro
		for _, p := range []string{"blob.bin", "media.txt", "grande.txt"} {
			r := g.gs("alice", P, "blame", p)
			if r.code != 1 || !strings.Contains(r.errOut, "blame non disponibile per "+p) || !strings.Contains(r.errOut, "1 MB") || r.out != "" {
				t.Errorf("blame di %s: %+v", p, r)
			}
		}
		if r := g.gs("alice", P, "blame", "non-esiste.go"); r.code != 6 {
			t.Errorf("file inesistente: %+v", r)
		}
		if r := g.gs("carol", P, "blame", "main.go"); r.code != 6 {
			t.Errorf("carol sul privato: %+v", r)
		}
	})

	t.Run("storico", func(t *testing.T) {
		type commit struct {
			Sha    string `json:"sha"`
			URL    string `json:"url"`
			Author struct {
				User *struct{ Kind string }
			} `json:"author"`
		}
		list := func(args ...string) []commit {
			t.Helper()
			r := g.ok("alice", P, append([]string{"blame", "--history", "--json", "sha,url,author"}, args...)...)
			var cs []commit
			if err := json.Unmarshal([]byte(r.out), &cs); err != nil {
				t.Fatal(err, r.out)
			}
			return cs
		}
		cs := list("main.go")
		if len(cs) != 2 || cs[0].Sha != g.shaBot || cs[1].Sha != g.shaFirst || !strings.HasSuffix(cs[0].URL, "/commit/"+g.shaBot) {
			t.Errorf("storico di main.go: %+v", cs)
		}
		if cs[0].Author.User == nil || cs[0].Author.User.Kind != "agent" {
			t.Errorf("badge agent: %+v", cs[0])
		}
		if cs := list("main.go", "--author", "botty@agents.example.com"); len(cs) != 1 || cs[0].Sha != g.shaBot {
			t.Errorf("--author: %+v", cs)
		}
		if cs := list("main.go", "--ref", "v0.1"); len(cs) != 1 || cs[0].Sha != g.shaFirst {
			t.Errorf("--ref v0.1: %+v", cs)
		}
		// -L: tronca e lo dice
		if r := g.ok("alice", P, "blame", "--history", "README.md", "-L", "1"); !strings.Contains(r.out, "primo commit") {
			t.Errorf("tabella: %s", r.out)
		}
		t2 := g.ok("alice", P, "blame", "--history", "main.go", "-L", "1")
		if !strings.Contains(t2.out, "botty [agent]") || !strings.Contains(t2.errOut, "Mostrati i primi 1 commit") {
			t.Errorf("limite: %+v", t2)
		}
		// un file che non esiste non ha storico, ma non è un errore
		if cs := list("non-esiste.go"); len(cs) != 0 {
			t.Errorf("file inesistente: %+v", cs)
		}
		if r := g.gs("carol", P, "blame", "--history", "main.go"); r.code != 6 {
			t.Errorf("carol sul privato: %+v", r)
		}
	})

	t.Run("browse", func(t *testing.T) {
		u := func(args ...string) string {
			t.Helper()
			return strings.TrimSpace(g.ok("alice", P, append([]string{"browse", "--no-browser"}, args...)...).out)
		}
		base := g.proxy + "/alice/" + P
		for args, want := range map[string]string{
			"":                        base,
			"main.go:5":               base + "/blob/main/main.go#L5",
			"docs/":                   base + "/tree/main/docs",
			"#1":                      base + "/issues/1",
			"--history main.go":       base + "/commits/main/main.go",
			"--blame main.go -b v0.1": base + "/blame/v0.1/main.go",
		} {
			if got := u(strings.Fields(args)...); got != want {
				t.Errorf("gs browse %s: %s, voluto %s", args, got, want)
			}
		}
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
