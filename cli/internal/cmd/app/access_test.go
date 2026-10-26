package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/app"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// GIT-376 (H6): gs app access list|add|remove|invite contro un gateway finto.

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type call struct {
	Method, Path string
	Body         map[string]any
}

type reply struct {
	status int
	body   string
}

type fake struct {
	mu     sync.Mutex
	routes map[string]reply
	calls  []call
	srv    *httptest.Server
}

func newFake(t *testing.T, routes map[string]reply) *fake {
	t.Helper()
	fk := &fake{routes: routes}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, body})
		fk.mu.Unlock()
		rep, ok := fk.routes[r.Method+" "+path]
		if !ok {
			rep = reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			rep = reply{401, `{"error":{"code":"unauthenticated","message":"serve autenticazione"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

func (fk *fake) find(method, path string) []call {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	var out []call
	for _, c := range fk.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	code        int
	out, errOut string
}

func runWith(t *testing.T, fk *fake, tty bool, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	io_.InTTY = tty
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok", "GS_HOST": fk.srv.URL}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", fk.srv.URL, "-R", "acme/web"}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

func run(t *testing.T, fk *fake, args ...string) result { return runWith(t, fk, false, "", args...) }

const (
	base       = "/repos/acme/web/apps/sito/access"
	idGVerdi   = "00000000-0000-0000-0000-0000000000a1"
	idBusiness = "00000000-0000-0000-0000-0000000000a2"
	userJSON   = `{"id":"` + idGVerdi + `","kind":"user","name":"gverdi","email":"giulia.verdi@retail.it","addedBy":{"id":"00000000-0000-0000-0000-000000000001","username":"mrossi","type":"human"},"addedAt":"2026-10-26T10:00:00Z"}`
	teamJSON   = `{"id":"` + idBusiness + `","kind":"team","name":"retail/business","memberCount":4,"addedAt":"2026-10-26T10:00:00Z"}`
	forbidden  = `{"error":{"code":"forbidden","message":"Serve il permesso admin sul repo."}}`
)

func TestAccessList(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + base + "/members": {200, `{"items":[` + teamJSON + `,` + userJSON + `]}`}})
	r := run(t, fk, "app", "access", "list", "sito")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	// Senza TTY le tabelle non hanno intestazione: una riga a tab per membro.
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "team\tretail/business\t4 persone\t") || !strings.HasPrefix(lines[1], "user\tgverdi\tgiulia.verdi@retail.it\tmrossi\t") {
		t.Fatalf("righe: %q", r.out)
	}
	r = run(t, fk, "app", "access", "list", "sito", "--json", "name,kind")
	if r.code != 0 || !strings.Contains(r.out, `"retail/business"`) || !strings.Contains(r.out, `"gverdi"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestAccessList_SoloAdmin(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + base + "/members": {403, forbidden}})
	if r := run(t, fk, "app", "access", "list", "sito"); r.code == 0 || !strings.Contains(r.errOut, "admin") {
		t.Fatalf("write/read devono essere rifiutati: %+v", r)
	}
	fk = newFake(t, map[string]reply{"POST " + base + "/members": {403, forbidden}, "GET " + base + "/invite": {403, forbidden}})
	if r := run(t, fk, "app", "access", "add", "sito", "gverdi"); r.code == 0 {
		t.Fatalf("add: %+v", r)
	}
	if r := run(t, fk, "app", "access", "invite", "sito"); r.code == 0 || r.out != "" {
		t.Fatalf("invite: %+v", r)
	}
}

func TestAccessAdd_UtenteETeam(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST " + base + "/members": {201, userJSON}})
	r := run(t, fk, "app", "access", "add", "sito", "gverdi")
	if r.code != 0 || !strings.Contains(r.errOut, "Aggiunto gverdi") {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	if b := fk.find("POST", base+"/members")[0].Body; b["kind"] != "user" || b["name"] != "gverdi" {
		t.Fatalf("corpo utente: %v", b)
	}
	if r = run(t, fk, "app", "access", "add", "sito", "retail/business"); r.code != 0 {
		t.Fatalf("team: %+v", r)
	}
	if b := fk.find("POST", base+"/members")[1].Body; b["kind"] != "team" || b["name"] != "retail/business" {
		t.Fatalf("corpo team: %v", b)
	}
	// Già in lista: errore dell'API, exit 1.
	fk = newFake(t, map[string]reply{"POST " + base + "/members": {409, `{"error":{"code":"already_member","message":"Già nella lista di accesso dell'app."}}`}})
	if r = run(t, fk, "app", "access", "add", "sito", "gverdi"); r.code != 1 || !strings.Contains(r.errOut, "Già nella lista") {
		t.Fatalf("doppione: %+v", r)
	}
	for _, args := range [][]string{{"app", "access", "add", "sito"}, {"app", "access", "add"}, {"app", "access", "add", "sito", " "}} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
}

// Il remove toglie un accesso: conferma G8 (riscrivere il nome), senza TTY serve --yes.
func TestAccessRemove_ConfermeG8(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET " + base + "/members":                  {200, `{"items":[` + teamJSON + `,` + userJSON + `]}`},
		"DELETE " + base + "/members/" + idGVerdi:   {204, ``},
		"DELETE " + base + "/members/" + idBusiness: {204, ``},
	})
	del := func(id string) int { return len(fk.find("DELETE", base+"/members/"+id)) }
	// Senza TTY e senza --yes: exit 2, nessuna chiamata alla lista (oltre a GET /meta, che ogni comando fa).
	if r := run(t, fk, "app", "access", "remove", "sito", "gverdi"); r.code != 2 || len(fk.find("GET", base+"/members")) != 0 {
		t.Fatalf("senza tty: exit %d, chiamate %d", r.code, len(fk.calls))
	}
	// TTY, risposta sbagliata: annullato (exit 1), nessuna DELETE.
	if r := runWith(t, fk, true, "nope\n", "app", "access", "remove", "sito", "gverdi"); r.code != 1 || del(idGVerdi) != 0 {
		t.Fatalf("risposta sbagliata: exit %d (%s)", r.code, r.errOut)
	}
	// TTY, nome riscritto: toglie l'utente.
	if r := runWith(t, fk, true, "gverdi\n", "app", "access", "remove", "sito", "gverdi"); r.code != 0 || del(idGVerdi) != 1 || !strings.Contains(r.errOut, "Tolto gverdi") {
		t.Fatalf("conferma: exit %d (%s)", r.code, r.errOut)
	}
	// --yes senza TTY: toglie il team (nome in qualunque maiuscola).
	if r := run(t, fk, "app", "access", "rm", "sito", "Retail/Business", "-y"); r.code != 0 || del(idBusiness) != 1 {
		t.Fatalf("--yes: exit %d (%s)", r.code, r.errOut)
	}
	// Uno che non è in lista: errore, nessuna DELETE in più.
	if r := run(t, fk, "app", "access", "remove", "sito", "nessuno", "--yes"); r.code != 1 || !strings.Contains(r.errOut, "non è nella lista") || del(idGVerdi)+del(idBusiness) != 2 {
		t.Fatalf("sconosciuto: %+v", r)
	}
}

const inviteText = "Hi, you can now test sito (acme/web).\n\n" +
	"1. Open https://git.example.com/help/ca?app=sito-web-acme.apps.example.com and follow the steps for your device.\n" +
	"   Your browser may warn you on that page: continue only if the page shows this fingerprint:\n" +
	"   SHA-256 4F:9A:21:C7:8E:03:B2\n" +
	"2. Open https://sito-web-acme.apps.example.com and sign in with your GitStack account.\n" +
	"3. Found a problem? Use \"Report a problem\" inside the app."

func TestAccessInvite_StampaLoStessoTesto(t *testing.T) {
	invite := map[string]any{
		"guideUrl": "https://git.example.com/help/ca?app=sito-web-acme.apps.example.com", "fingerprintSha256": "4F:9A:21:C7:8E:03:B2",
		"address": "https://sito-web-acme.apps.example.com", "caMode": "internal", "text": inviteText,
	}
	b, _ := json.Marshal(invite)
	fk := newFake(t, map[string]reply{"GET " + base + "/invite": {200, string(b)}})
	r := run(t, fk, "app", "access", "invite", "sito")
	if r.code != 0 || r.out != inviteText+"\n" {
		t.Fatalf("exit %d: %q | %s", r.code, r.out, r.errOut)
	}
	r = run(t, fk, "app", "access", "invite", "sito", "--json", "fingerprintSha256,address")
	if r.code != 0 || !strings.Contains(r.out, `"4F:9A:21:C7:8E:03:B2"`) || !strings.Contains(r.out, `"https://sito-web-acme.apps.example.com"`) {
		t.Fatalf("json: %+v", r)
	}
	// L'app senza indirizzo: l'errore dell'API, niente testo.
	fk = newFake(t, map[string]reply{"GET " + base + "/invite": {409, `{"error":{"code":"no_address","message":"L'app non ha un indirizzo."}}`}})
	if r = run(t, fk, "app", "access", "invite", "sito"); r.code != 1 || r.out != "" {
		t.Fatalf("no_address: %+v", r)
	}
}

func TestCampiJsonDocumentati(t *testing.T) {
	if len(app.AccessFields) != 7 || len(app.InviteFields) != 5 {
		t.Fatalf("campi: %v %v", app.AccessFields, app.InviteFields)
	}
}
