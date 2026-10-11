package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type result struct {
	code        int
	out, errOut string
}

type seen struct {
	method, path, query, ctype, custom string
	body                               string
}

func harness(t *testing.T, stdin string, h http.HandlerFunc) (func(args ...string) result, *[]seen) {
	t.Helper()
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/meta") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"dev"}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("X-Prova"), string(b)})
		mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return func(args ...string) result {
		io_, in, out, errOut := cmdutil.Test()
		in.WriteString(stdin)
		f := cmdutil.New("dev", io_)
		cfg := t.TempDir()
		f.Getenv = func(k string) string {
			return map[string]string{"GS_HOST": srv.URL, "GS_TOKEN": "gst_x", config.EnvConfigDir: cfg}[k]
		}
		f.Git = noGit{}
		f.HTTPClient = srv.Client
		code := root.Run(context.Background(), f, args)
		return result{code, out.String(), errOut.String()}
	}, &reqs
}

func jsonOK(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

func TestGetEPercorsi(t *testing.T) {
	run, reqs := harness(t, "", jsonOK(`{"ok":true}`))
	for _, p := range []string{"/orgs", "orgs", "/api/v1/orgs"} {
		if r := run("api", p); r.code != 0 || strings.TrimSpace(r.out) != `{"ok":true}` {
			t.Errorf("%s: %+v", p, r)
		}
	}
	for _, s := range *reqs {
		if s.method != "GET" || s.path != "/api/v1/orgs" {
			t.Errorf("richiesta: %+v", s)
		}
	}
	// La query nel percorso si mantiene e si unisce ai campi di un GET.
	run("api", "/orgs?state=all&x=1", "-X", "GET", "-F", "page=2")
	if s := (*reqs)[len(*reqs)-1]; s.path != "/api/v1/orgs" || s.query != "page=2&state=all&x=1" {
		t.Errorf("query nel percorso: %+v", s)
	}
	// Uso errato: nessun percorso, indirizzo completo, metodo e --input con campi.
	n := len(*reqs)
	for _, args := range [][]string{
		{"api"}, {"api", "http://altro.test/orgs"}, {"api", "/x", "-X", "po st"},
		{"api", "/x", "-f", "senzaValore"}, {"api", "/x", "-f", "a=1", "--input", "-"},
		{"api", "/x", "-X", "POST", "--paginate"}, {"api", "/x", "--jq", ".["},
	} {
		if r := run(args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2 (%s)", args, r.code, r.errOut)
		}
	}
	if len(*reqs) != n {
		t.Errorf("un uso errato ha chiamato la rete")
	}
}

func TestCampiEMetodo(t *testing.T) {
	run, reqs := harness(t, "", jsonOK(`{}`))
	// -f/-F => POST con corpo JSON tipizzato e annidato.
	r := run("api", "/orgs", "-f", "name=acme", "-F", "n=42", "-F", "ok=true", "-F", "z=null", "-f", "s=42", "-f", "o[k]=v", "-F", "tags[]=a", "-F", "tags[]=b", "-H", "X-Prova: si")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	s := (*reqs)[0]
	if s.method != "POST" || s.ctype != "application/json" || s.custom != "si" {
		t.Errorf("richiesta: %+v", s)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(s.body), &got); err != nil {
		t.Fatal(err, s.body)
	}
	tags, _ := got["tags"].([]any)
	if got["name"] != "acme" || got["n"] != float64(42) || got["ok"] != true || got["z"] != nil || got["s"] != "42" ||
		got["o"].(map[string]any)["k"] != "v" || len(tags) != 2 {
		t.Errorf("corpo: %s", s.body)
	}
	// -X cambia il metodo; GET con campi => query.
	run("api", "/orgs/acme", "-X", "PATCH", "-f", "displayName=Acme")
	if s := (*reqs)[1]; s.method != "PATCH" || !strings.Contains(s.body, `"displayName":"Acme"`) {
		t.Errorf("PATCH: %+v", s)
	}
	run("api", "/search", "-X", "GET", "-f", "q=is:open", "-F", "page=2")
	if s := (*reqs)[2]; s.method != "GET" || s.body != "" || s.query != "page=2&q=is%3Aopen" {
		t.Errorf("GET con campi: %+v", s)
	}
}

func TestCorpoDaStdinEFile(t *testing.T) {
	run, reqs := harness(t, `{"url":"https://x.test/hook"}`, jsonOK(`{}`))
	if r := run("api", "/orgs/acme/webhooks", "--input", "-"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if s := (*reqs)[0]; s.method != "POST" || s.body != `{"url":"https://x.test/hook"}` {
		t.Errorf("--input -: %+v", s)
	}
	// -F chiave=@- legge il valore da stdin.
	run2, reqs2 := harness(t, "testo\ndue righe\n", jsonOK(`{}`))
	run2("api", "/x", "-F", "body=@-")
	if s := (*reqs2)[0]; !strings.Contains(s.body, `"body":"testo\ndue righe\n"`) {
		t.Errorf("@-: %+v", s)
	}
}

func TestPaginateEJq(t *testing.T) {
	run, reqs := harness(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = io.WriteString(w, `{"items":[{"n":1},{"n":2}],"page":1,"perPage":2,"total":5}`)
		case "2":
			_, _ = io.WriteString(w, `{"items":[{"n":3},{"n":4}],"page":2,"perPage":2,"total":5}`)
		default:
			_, _ = io.WriteString(w, `{"items":[{"n":5}],"page":3,"perPage":2,"total":5}`)
		}
	})
	r := run("api", "/things", "--paginate", "--jq", "[.[].n]")
	if r.code != 0 || strings.TrimSpace(r.out) != "[1,2,3,4,5]" {
		t.Fatalf("%+v", r)
	}
	if len(*reqs) != 3 {
		t.Errorf("richieste: %d", len(*reqs))
	}
	if r := run("api", "/things", "--paginate", "--jq", ".[] | .n"); r.out != "1\n2\n3\n4\n5\n" {
		t.Errorf("jq su più valori: %q", r.out)
	}
	// Una risposta senza items non è paginata: si stampa com'è.
	run2, _ := harness(t, "", jsonOK(`{"name":"x"}`))
	if r := run2("api", "/one", "--paginate", "--jq", ".name"); strings.TrimSpace(r.out) != "x" {
		t.Errorf("non paginata: %+v", r)
	}
}

func TestErroriCodiciECorpo(t *testing.T) {
	for _, c := range []struct {
		status, exit int
		code         string
	}{{401, 4, "unauthenticated"}, {403, 5, "forbidden"}, {404, 6, "not_found"}, {422, 1, "invalid"}} {
		body := `{"error":{"code":"` + c.code + `","message":"no"}}`
		run, _ := harness(t, "", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, body)
		})
		r := run("api", "/x")
		if r.code != c.exit || strings.TrimSpace(r.out) != body || !strings.HasPrefix(r.errOut, "gs: ") {
			t.Errorf("%d: %+v", c.status, r)
		}
		r = run("api", "/x", "--jq", ".")
		if r.code != c.exit || strings.TrimSpace(r.out) != body || !strings.Contains(r.errOut, `"code":"`+c.code+`"`) {
			t.Errorf("%d --jq: %+v", c.status, r)
		}
	}
}

func TestInclude(t *testing.T) {
	run, _ := harness(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Cosa", "si")
		_, _ = io.WriteString(w, `{"a":1}`)
	})
	r := run("api", "/x", "-i")
	if r.code != 0 || !strings.HasPrefix(r.out, "HTTP/1.1 200 OK\n") || !strings.Contains(r.out, "X-Cosa: si\n") || !strings.HasSuffix(strings.TrimSpace(r.out), `{"a":1}`) {
		t.Errorf("%q", r.out)
	}
}
