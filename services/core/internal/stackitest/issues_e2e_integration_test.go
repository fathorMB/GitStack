//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
)

// M-05 (GIT-113): le issues sullo stack completo, solo via API. Gateway,
// identity e git sono i binari veri, core è il router vero in-process, Postgres
// è reale. Utenti: alice (proprietaria, admin), bob (write), carol (solo read
// sul repo interno, nessun grant sul privato), dave (read sul privato) e
// l'agente botty (write sull'interno). Ogni caso negativo controlla che il
// corpo della risposta non porti dati del repo privato (issueLeak).

const (
	e2eInternal = "iss-int"
	e2ePrivate  = "iss-priv"

	// Dati del repo privato che chi non lo legge non deve mai vedere.
	privTitle = "ZAFFIRO-RISERVATO-91 migrazione del cluster"
	privBody  = "corpo riservato con CHIAVE-ARANCIO-77"
	privLabel = "etichetta-riservata-zeta"
	privFile  = "allegato-riservato-ametista.png"
)

// pngTiny: intestazione PNG valida quanto basta per il riconoscimento dei byte.
var pngTiny = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 40)...)

type issuesE2E struct {
	*gitEnv
	botID  string
	userID map[string]string
	privID string
	intID  string
}

// api chiama il gateway come user (token read+write). user "" = senza credenziali.
func (e *issuesE2E) api(user, method, path string, body any) reply {
	e.t.Helper()
	var hdr map[string]string
	if user != "" {
		hdr = e.bearer(user)
	}
	return e.gw(method, path, body, hdr, nil)
}

func (e *issuesE2E) must(user, method, path string, body any, status int) reply {
	e.t.Helper()
	r := e.api(user, method, path, body)
	if r.status != status {
		e.t.Fatalf("%s %s come %s: %d, voluto %d: %s", method, path, user, r.status, status, r.body)
	}
	return r
}

// deny: la richiesta è rifiutata con `status` e il corpo non porta dati riservati.
func (e *issuesE2E) deny(user, method, path string, body any, status int, code string) {
	e.t.Helper()
	r := e.api(user, method, path, body)
	if r.status != status {
		e.t.Errorf("%s %s come %s: %d, voluto %d: %s", method, path, user, r.status, status, r.body)
	}
	if code != "" && r.errCode() != code {
		e.t.Errorf("%s %s come %s: codice %q, voluto %q", method, path, user, r.errCode(), code)
	}
	e.issueLeak(fmt.Sprintf("%s %s come %s", method, path, user), string(r.body))
}

// issueLeak: nessun dato del repo privato nel corpo.
func (e *issuesE2E) issueLeak(what, body string) {
	e.t.Helper()
	for _, n := range []string{"ZAFFIRO", "CHIAVE-ARANCIO", privLabel, privFile, e2ePrivate, e.privID} {
		if strings.Contains(body, n) {
			e.t.Errorf("%s: trapela %q: %s", what, n, truncate([]byte(body)))
		}
	}
}

func issuePath(repo string, n any, tail string) string {
	return fmt.Sprintf("/repos/alice/%s/issues/%v%s", repo, n, tail)
}

func (e *issuesE2E) open(user, repo, title, body string, extra map[string]any) float64 {
	e.t.Helper()
	in := map[string]any{"title": title, "body": body}
	for k, v := range extra {
		in[k] = v
	}
	r := e.must(user, "POST", "/repos/alice/"+repo+"/issues", in, 201)
	n, _ := r.json()["number"].(float64)
	return n
}

// numbers estrae i numeri dagli elementi di una pagina di issues (items[].number
// o items[].issue.number per la ricerca globale).
func numbers(r reply) []int {
	var out struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(r.body, &out)
	var ns []int
	for _, it := range out.Items {
		if i, ok := it["issue"].(map[string]any); ok {
			it = i
		}
		n, _ := it["number"].(float64)
		ns = append(ns, int(n))
	}
	return ns
}

func has(ns []int, n float64) bool {
	for _, x := range ns {
		if x == int(n) {
			return true
		}
	}
	return false
}

func eventTypesOf(r reply) []string {
	var out struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	_ = json.Unmarshal(r.body, &out)
	var ts []string
	for _, it := range out.Items {
		ts = append(ts, it.Type)
	}
	return ts
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// upload manda un multipart al gateway; user "" = senza credenziali.
func (e *issuesE2E) upload(user, repo, filename string, content []byte) reply {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	h.Set("Content-Type", "text/html") // il tipo dichiarato non conta
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.gateway+"/v1/repos/alice/"+repo+"/issue-attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+e.tokens[user])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

func (e *issuesE2E) download(user, repo, id string) fullReply {
	e.t.Helper()
	var hdr map[string]string
	if user != "" {
		hdr = e.bearer(user)
	}
	return rawGet(e.t, e.gateway+"/v1/repos/alice/"+repo+"/issue-attachments/"+id, hdr)
}

func newIssuesE2E(t *testing.T) *issuesE2E {
	t.Helper()
	disk := &attachments.Disk{Dir: t.TempDir()}
	g := newGitEnv(t, httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: disk, MaxBytes: 1 << 20}))
	e := &issuesE2E{gitEnv: g, userID: map[string]string{}}

	// Utenti in più, creati dall'admin: dave (umano) e botty (agente).
	login := e.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, login, 200, "")
	admin := e.session(login)
	const userPassword = "password-di-prova-molto-lunga-1"
	u := e.gw("POST", "/users", map[string]any{"username": "dave", "email": "dave@example.com", "password": userPassword}, nil, admin)
	want(t, u, 201, "")
	e.userID["dave"], _ = u.json()["id"].(string)
	l := e.gw("POST", "/auth/login", map[string]any{"username": "dave", "password": userPassword}, nil, nil)
	want(t, l, 200, "")
	tok := e.gw("POST", "/user/tokens", map[string]any{"name": "rw", "scopes": []string{"read:resource", "write:resource"},
		"expiresAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}, nil, e.session(l))
	want(t, tok, 201, "")
	e.tokens["dave"], _ = tok.json()["token"].(string)
	b := e.gw("POST", "/users", map[string]any{"username": "botty", "kind": "agent", "email": "botty@agents.example.com"}, nil, admin)
	want(t, b, 201, "")
	e.userID["botty"], _ = b.json()["id"].(string)
	for _, name := range []string{"bob"} {
		r := e.gw("GET", "/users/"+name, nil, nil, admin)
		want(t, r, 200, "")
		e.userID[name], _ = r.json()["id"].(string)
	}

	// Repo e grant: bob write su entrambi, botty write sull'interno, dave read sul privato.
	e.intID = e.createRepo("alice", e2eInternal, "internal")
	e.privID = e.createRepo("alice", e2ePrivate, "private")
	grant := func(repoID, user, role string) {
		want(t, e.gw("POST", "/resources/"+repoID+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID[user], "role": role}, e.bearer("alice"), nil), 201, "")
	}
	grant(e.intID, "bob", "write")
	grant(e.privID, "bob", "write")
	grant(e.intID, "botty", "write")
	grant(e.privID, "dave", "read")
	return e
}

// TestIssuesE2E è il percorso completo delle issues (I1–I11, R10) sullo stack.
func TestIssuesE2E(t *testing.T) {
	e := newIssuesE2E(t)
	I := e2eInternal
	P := e2ePrivate

	// Un modello nel repo interno, spinto con git vero (I11).
	t.Run("preparazione_modelli", func(t *testing.T) {
		work := e.initWork(e.httpsURL("alice", e.tokens["alice"], "/alice/"+I+".git"), map[string]string{
			"README.md": "# prova\n",
			".gitstack/ISSUE_TEMPLATE/bug.md": "---\ntitle: Bug Report\nabout: Segnala un bug\nlabels:\n  - bug\n---\n\n## Passi per riprodurre\n",
		})
		e.mustGit(work, "", "push", "-q", "origin", "main")
	})

	// --- ciclo di vita: apertura, commenti, etichette, milestone, assegnazione ---
	var n1, n2, n3, n4 float64
	t.Run("ciclo_di_vita", func(t *testing.T) {
		// chi ha solo read apre (I3) e la numerazione parte da 1 (I1)
		n1 = e.open("carol", I, "Crash all'avvio", "stack trace", nil)
		if n1 != 1 {
			t.Fatalf("prima issue = #%v, voluto #1", n1)
		}
		r := e.must("carol", "GET", issuePath(I, n1, ""), nil, 200)
		if r.json()["state"] != "open" || r.json()["locked"] != false {
			t.Errorf("issue aperta: %s", r.body)
		}
		if a, _ := r.json()["author"].(map[string]any); a["username"] != "carol" {
			t.Errorf("autore: %s", r.body)
		}
		// commenti: del lettore e di chi scrive
		e.must("carol", "POST", issuePath(I, n1, "/comments"), map[string]any{"body": "succede sempre"}, 201)
		e.must("bob", "POST", issuePath(I, n1, "/comments"), map[string]any{"body": "riproduco, **grazie**"}, 201)
		if c := e.must("carol", "GET", issuePath(I, n1, "/comments"), nil, 200); !strings.Contains(string(c.body), "riproduco") {
			t.Errorf("commenti: %s", c.body)
		}
		// etichette: le predefinite ci sono (I5), solo write le assegna
		labels := e.must("carol", "GET", "/repos/alice/"+I+"/labels", nil, 200)
		if labels.json()["total"] != float64(8) {
			t.Errorf("etichette predefinite: %s", labels.body)
		}
		e.must("bob", "POST", "/repos/alice/"+I+"/labels", map[string]any{"name": "area-rete", "color": "1d76db"}, 201)
		r = e.must("bob", "PUT", issuePath(I, n1, "/labels"), map[string]any{"labels": []string{"bug", "area-rete"}}, 200)
		if !strings.Contains(string(r.body), "area-rete") {
			t.Errorf("etichette: %s", r.body)
		}
		// milestone per repo (I7)
		e.must("bob", "POST", "/repos/alice/"+I+"/milestones", map[string]any{"title": "v1", "dueOn": "2027-01-31"}, 201)
		e.must("bob", "PUT", issuePath(I, n1, "/milestone"), map[string]any{"milestone": 1}, 200)
		// assegnazione a un agente con write (I6)
		r = e.must("bob", "PUT", issuePath(I, n1, "/assignees"), map[string]any{"assignees": []string{"botty"}}, 200)
		as, _ := r.json()["assignees"].([]any)
		if len(as) != 1 || as[0].(map[string]any)["kind"] != "agent" {
			t.Errorf("assegnatari: %s", r.body)
		}
		// modifica del titolo: tracciata (I4)
		e.deny("bob", "PATCH", issuePath(I, n1, ""), map[string]any{"title": "altrui"}, 403, "forbidden") // solo l'autore
		e.must("carol", "PATCH", issuePath(I, n1, ""), map[string]any{"title": "Crash all'avvio (v1)"}, 200)
		ts := eventTypesOf(e.must("carol", "GET", issuePath(I, n1, "/events"), nil, 200))
		for _, want := range []string{"opened", "labeled", "milestoned", "assigned", "renamed"} {
			if !containsStr(ts, want) {
				t.Errorf("cronologia senza %q: %v", want, ts)
			}
		}
		e.deny("carol", "GET", issuePath(I, n1, "/versions"), nil, 403, "forbidden") // le versioni sono dell'admin
		if v := e.must("alice", "GET", issuePath(I, n1, "/versions"), nil, 200); !strings.Contains(string(v.body), "Crash all") {
			t.Errorf("versioni: %s", v.body)
		}
		// la milestone conta l'avanzamento
		m := e.must("carol", "GET", "/repos/alice/"+I+"/milestones/1", nil, 200)
		if m.json()["openIssues"] != float64(1) || m.json()["closedIssues"] != float64(0) {
			t.Errorf("milestone: %s", m.body)
		}
	})

	t.Run("chiusura_con_ogni_motivo_e_riapertura", func(t *testing.T) {
		n2 = e.open("bob", I, "Da completare", "", nil)
		n3 = e.open("bob", I, "Fuori programma", "", nil)
		n4 = e.open("bob", I, "Doppione", "", nil)
		if n2 != 2 || n3 != 3 || n4 != 4 {
			t.Fatalf("numerazione I1: #%v #%v #%v", n2, n3, n4)
		}
		r := e.must("bob", "POST", issuePath(I, n2, "/close"), map[string]any{}, 200)
		if r.json()["state"] != "closed" || r.json()["closeReason"] != "completed" {
			t.Errorf("completed: %s", r.body)
		}
		r = e.must("bob", "POST", issuePath(I, n3, "/close"), map[string]any{"reason": "not_planned"}, 200)
		if r.json()["closeReason"] != "not_planned" {
			t.Errorf("not_planned: %s", r.body)
		}
		r = e.must("bob", "POST", issuePath(I, n4, "/close"), map[string]any{"reason": "duplicate", "duplicateOf": 1}, 200)
		if r.json()["closeReason"] != "duplicate" || r.json()["duplicateOf"] != float64(1) {
			t.Errorf("duplicate: %s", r.body)
		}
		e.deny("bob", "POST", issuePath(I, n3, "/close"), map[string]any{}, 409, "already_closed")
		e.deny("bob", "POST", issuePath(I, n1, "/close"), map[string]any{"reason": "duplicate"}, 422, "validation_failed")
		// riapertura
		r = e.must("bob", "POST", issuePath(I, n2, "/reopen"), nil, 200)
		if r.json()["state"] != "open" {
			t.Errorf("riapertura: %s", r.body)
		}
		e.deny("bob", "POST", issuePath(I, n2, "/reopen"), nil, 409, "already_open")
		ts := eventTypesOf(e.must("bob", "GET", issuePath(I, n2, "/events"), nil, 200))
		if !containsStr(ts, "closed") || !containsStr(ts, "reopened") {
			t.Errorf("cronologia: %v", ts)
		}
		// lo stato filtra l'elenco
		if ns := numbers(e.must("carol", "GET", "/repos/alice/"+I+"/issues?state=closed", nil, 200)); has(ns, n2) || !has(ns, n3) || !has(ns, n4) {
			t.Errorf("chiuse = %v", ns)
		}
	})

	// --- I4: le issues non si eliminano ---------------------------------------
	t.Run("issue_non_eliminabili_I4", func(t *testing.T) {
		for _, u := range []string{"alice", "bob", "carol"} {
			if r := e.api(u, "DELETE", issuePath(I, n1, ""), nil); r.status < 400 {
				t.Errorf("DELETE issue come %s: %d, la issue non è eliminabile", u, r.status)
			}
		}
		e.must("carol", "GET", issuePath(I, n1, ""), nil, 200)
	})

	// --- I3: matrice dei permessi su repo interno e privato --------------------
	t.Run("permessi_I3", func(t *testing.T) {
		own := e.open("carol", I, "Issue di carol", "", nil)
		// read: solo le sue cose; niente etichette, assegnatari, milestone
		e.deny("carol", "POST", "/repos/alice/"+I+"/issues", map[string]any{"title": "x", "labels": []string{"bug"}}, 403, "forbidden")
		e.deny("carol", "POST", "/repos/alice/"+I+"/issues", map[string]any{"title": "x", "assignees": []string{"bob"}}, 403, "forbidden")
		e.deny("carol", "PUT", issuePath(I, n1, "/labels"), map[string]any{"labels": []string{"bug"}}, 403, "forbidden")
		e.deny("carol", "PUT", issuePath(I, n1, "/assignees"), map[string]any{"assignees": []string{"carol"}}, 403, "forbidden")
		e.deny("carol", "PUT", issuePath(I, n1, "/milestone"), map[string]any{"milestone": 1}, 403, "forbidden")
		e.deny("carol", "POST", issuePath(I, n2, "/close"), map[string]any{}, 403, "forbidden") // issue di bob
		e.deny("carol", "POST", "/repos/alice/"+I+"/labels", map[string]any{"name": "x", "color": "ff0000"}, 403, "forbidden")
		e.deny("carol", "POST", "/repos/alice/"+I+"/milestones", map[string]any{"title": "x"}, 403, "forbidden")
		e.deny("carol", "PATCH", issuePath(I, n2, ""), map[string]any{"title": "altrui"}, 403, "forbidden")
		e.must("carol", "POST", issuePath(I, own, "/close"), map[string]any{}, 200) // chiude la propria
		e.must("carol", "POST", issuePath(I, own, "/reopen"), nil, 200)
		// write gestisce, ma non nasconde né blocca
		e.deny("bob", "PUT", issuePath(I, n1, "/lock"), map[string]any{}, 403, "forbidden")
		e.deny("bob", "PUT", issuePath(I, n1, "/hidden"), map[string]any{"hidden": true}, 403, "forbidden")
		// admin sì
		e.must("alice", "PUT", issuePath(I, n1, "/lock"), map[string]any{"reason": "troppo accesa"}, 200)
		e.must("alice", "DELETE", issuePath(I, n1, "/lock"), nil, 200)

		// repo privato: alice scrive il dato riservato; read (dave) legge e apre,
		// chi non ha grant (carol) non sa nemmeno che esiste (404 senza dati)
		pn := e.open("alice", P, privTitle, privBody, nil)
		e.must("alice", "POST", "/repos/alice/"+P+"/labels", map[string]any{"name": privLabel, "color": "ff00ff"}, 201)
		e.must("alice", "PUT", issuePath(P, pn, "/labels"), map[string]any{"labels": []string{privLabel}}, 200)
		e.must("dave", "GET", issuePath(P, pn, ""), nil, 200)
		e.must("dave", "POST", issuePath(P, pn, "/comments"), map[string]any{"body": "letto da dave"}, 201)
		e.must("dave", "POST", "/repos/alice/"+P+"/issues", map[string]any{"title": "da dave"}, 201)
		e.deny("dave", "POST", issuePath(P, pn, "/close"), map[string]any{}, 403, "forbidden")
		e.deny("dave", "PUT", issuePath(P, pn, "/labels"), map[string]any{"labels": []string{"bug"}}, 403, "forbidden")
		for _, c := range []struct {
			method, tail string
			body         any
		}{
			{"GET", "", nil}, {"GET", "/comments", nil}, {"GET", "/events", nil}, {"GET", "/versions", nil},
			{"POST", "/comments", map[string]any{"body": "x"}}, {"POST", "/close", map[string]any{}},
			{"POST", "/reopen", nil}, {"PUT", "/labels", map[string]any{"labels": []string{"bug"}}},
			{"PUT", "/assignees", map[string]any{"assignees": []string{"carol"}}},
			{"PUT", "/milestone", map[string]any{"milestone": 1}}, {"PUT", "/lock", map[string]any{}},
			{"PUT", "/hidden", map[string]any{"hidden": true}}, {"PATCH", "", map[string]any{"title": "x"}},
		} {
			e.deny("carol", c.method, issuePath(P, pn, c.tail), c.body, 404, "not_found")
		}
		for _, p := range []string{"/issues", "/issues?state=all", "/labels", "/milestones", "/issue-templates", "/issues/999"} {
			e.deny("carol", "GET", "/repos/alice/"+P+p, nil, 404, "not_found")
		}
		e.deny("carol", "POST", "/repos/alice/"+P+"/issues", map[string]any{"title": "x"}, 404, "not_found")
		// un repo inesistente risponde allo stesso modo
		miss := e.api("carol", "GET", "/repos/alice/non-esiste/issues", nil)
		priv := e.api("carol", "GET", "/repos/alice/"+P+"/issues", nil)
		if miss.status != priv.status || miss.errCode() != priv.errCode() {
			t.Errorf("privato %d/%s diverso da inesistente %d/%s", priv.status, priv.errCode(), miss.status, miss.errCode())
		}
		// senza credenziali: 401 su ogni lettura e scrittura
		for _, p := range []string{"/repos/alice/" + I + "/issues", issuePath(I, n1, ""), "/repos/alice/" + P + "/issues", "/search/issues?q=x"} {
			e.deny("", "GET", p, nil, 401, "")
		}
		e.deny("", "POST", "/repos/alice/"+I+"/issues", map[string]any{"title": "x"}, 401, "")
		// token di sola lettura: legge, non scrive
		ro := map[string]string{"Authorization": "Bearer " + e.roToken["bob"]}
		if r := e.gw("GET", issuePath(I, n1, ""), nil, ro, nil); r.status != 200 {
			t.Errorf("lettura con token ro: %d", r.status)
		}
		if r := e.gw("POST", issuePath(I, n1, "/comments"), map[string]any{"body": "x"}, ro, nil); r.status != 403 {
			t.Errorf("scrittura con token ro: %d, voluto 403", r.status)
		}
	})

	// --- I11: blocco della discussione, issue nascoste --------------------------
	t.Run("blocco_I11", func(t *testing.T) {
		n := e.open("bob", I, "Discussione accesa", "", nil)
		e.must("alice", "PUT", issuePath(I, n, "/lock"), map[string]any{"reason": "troppo accesa"}, 200)
		e.must("alice", "PUT", issuePath(I, n, "/lock"), map[string]any{}, 200) // idempotente
		if r := e.must("carol", "GET", issuePath(I, n, ""), nil, 200); r.json()["locked"] != true {
			t.Errorf("bloccata: %s", r.body)
		}
		e.deny("carol", "POST", issuePath(I, n, "/comments"), map[string]any{"body": "ancora io"}, 403, "locked")
		e.must("bob", "POST", issuePath(I, n, "/comments"), map[string]any{"body": "chi ha write può"}, 201)
		e.must("alice", "DELETE", issuePath(I, n, "/lock"), nil, 200)
		e.must("carol", "POST", issuePath(I, n, "/comments"), map[string]any{"body": "di nuovo"}, 201)
	})

	t.Run("issue_nascosta_I4", func(t *testing.T) {
		n := e.open("bob", I, "Contenuto improprio NASCOSTO-5521", "", nil)
		e.must("alice", "PUT", issuePath(I, n, "/hidden"), map[string]any{"hidden": true}, 200)
		for _, u := range []string{"bob", "carol"} {
			e.deny(u, "GET", issuePath(I, n, ""), nil, 404, "not_found")
			e.deny(u, "GET", issuePath(I, n, "/comments"), nil, 404, "not_found")
			e.deny(u, "POST", issuePath(I, n, "/comments"), map[string]any{"body": "x"}, 404, "not_found")
			for _, p := range []string{"/repos/alice/" + I + "/issues?state=all", "/repos/alice/" + I + "/issues?q=NASCOSTO-5521", "/search/issues?q=NASCOSTO-5521"} {
				r := e.must(u, "GET", p, nil, 200)
				if has(numbers(r), n) || strings.Contains(string(r.body), "NASCOSTO-5521") {
					t.Errorf("%s vede la issue nascosta in %s: %s", u, p, truncate(r.body))
				}
			}
		}
		e.must("alice", "GET", issuePath(I, n, ""), nil, 200)
		if r := e.must("alice", "GET", "/search/issues?q=NASCOSTO-5521", nil, 200); !has(numbers(r), n) {
			t.Errorf("l'admin non la trova: %s", r.body)
		}
		e.must("alice", "PUT", issuePath(I, n, "/hidden"), map[string]any{"hidden": false}, 200)
		e.must("carol", "GET", issuePath(I, n, ""), nil, 200)
	})

	// --- I9: allegati protetti --------------------------------------------------
	t.Run("allegati_I9", func(t *testing.T) {
		r := e.upload("bob", I, "../../schermata.png", pngTiny)
		if r.status != 201 {
			t.Fatalf("upload: %d %s", r.status, r.body)
		}
		id, _ := r.json()["id"].(string)
		if r.json()["contentType"] != "image/png" || r.json()["filename"] != "schermata.png" {
			t.Errorf("metadati: %s", r.body)
		}
		n := e.open("bob", I, "Con allegato", "vedi sotto", map[string]any{"attachmentIds": []string{id}})
		// chi legge scarica, mai come pagina eseguibile
		d := e.download("carol", I, id)
		if d.status != 200 || d.header.Get("Content-Type") != "image/png" || !strings.HasPrefix(d.header.Get("Content-Disposition"), "attachment") ||
			d.header.Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(d.body, pngTiny) {
			t.Errorf("download: %d %v", d.status, d.header)
		}
		if i := e.must("carol", "GET", issuePath(I, n, ""), nil, 200); !strings.Contains(string(i.body), id) {
			t.Errorf("l'issue non porta l'allegato: %s", i.body)
		}
		// niente URL pubblici
		if d := e.download("", I, id); d.status != 401 {
			t.Errorf("anonimo: %d, voluto 401", d.status)
		}
		// tipi non ammessi (dai byte) e campo sbagliato
		for name, content := range map[string][]byte{
			"pagina.html": []byte("<!DOCTYPE html><html><script>alert(1)</script></html>"),
			"img.svg":     []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
			"app.exe":     append([]byte("MZ"), bytes.Repeat([]byte{0, 1, 2, 3}, 20)...),
		} {
			if r := e.upload("bob", I, name, content); r.status != 422 {
				t.Errorf("%s: %d, voluto 422: %s", name, r.status, r.body)
			}
		}
		if r := e.upload("bob", I, "grande.log", append([]byte("log\n"), bytes.Repeat([]byte("x"), 2<<20)...)); r.status != 413 {
			t.Errorf("oltre il limite: %d, voluto 413", r.status)
		}
		if r := e.upload("", I, "a.png", pngTiny); r.status != 401 {
			t.Errorf("upload anonimo: %d", r.status)
		}
		// repo privato: chi non lo legge non carica né scarica, e non vede il nome
		pr := e.upload("alice", P, privFile, pngTiny)
		if pr.status != 201 {
			t.Fatalf("upload privato: %d %s", pr.status, pr.body)
		}
		pid, _ := pr.json()["id"].(string)
		pn := e.open("alice", P, "con allegato privato", "", map[string]any{"attachmentIds": []string{pid}})
		_ = pn
		if r := e.upload("carol", P, "a.png", pngTiny); r.status != 404 {
			t.Errorf("upload su privato senza grant: %d, voluto 404", r.status)
		} else {
			e.issueLeak("upload privato carol", string(r.body))
		}
		if d := e.download("carol", P, pid); d.status != 404 {
			t.Errorf("download privato senza grant: %d, voluto 404", d.status)
		} else {
			e.issueLeak("download privato carol", string(d.body))
		}
		// un allegato del repo privato non si scarica passando dall'altro repo
		if d := e.download("carol", I, pid); d.status != 404 {
			t.Errorf("allegato di un altro repo: %d, voluto 404", d.status)
		}
		if d := e.download("dave", P, pid); d.status != 200 {
			t.Errorf("dave (read) non scarica: %d", d.status)
		}
		// issue nascosta: l'allegato sparisce per chi non è admin
		e.must("alice", "PUT", issuePath(I, n, "/hidden"), map[string]any{"hidden": true}, 200)
		if d := e.download("carol", I, id); d.status != 404 {
			t.Errorf("allegato di issue nascosta: %d, voluto 404", d.status)
		}
		if d := e.download("alice", I, id); d.status != 200 {
			t.Errorf("l'admin non scarica: %d", d.status)
		}
	})

	// --- I11: modelli dal repo -------------------------------------------------
	t.Run("modelli_I11", func(t *testing.T) {
		r := e.must("carol", "GET", "/repos/alice/"+I+"/issue-templates", nil, 200)
		if !strings.Contains(string(r.body), "Bug Report") || !strings.Contains(string(r.body), "Passi per riprodurre") {
			t.Errorf("modelli: %s", r.body)
		}
		e.deny("carol", "GET", "/repos/alice/"+P+"/issue-templates", nil, 404, "not_found")
		// trasferimento fuori dalla v1: nessuna rotta
		if r := e.api("alice", "POST", issuePath(I, n1, "/transfer"), map[string]any{"repo": P}); r.status < 400 {
			t.Errorf("trasferimento: %d, non esiste nella v1", r.status)
		}
	})

	// --- I10: ricerca nel repo e su tutta l'installazione ------------------------
	t.Run("ricerca_I10", func(t *testing.T) {
		q := func(user, path, query string) reply {
			return e.must(user, "GET", path+"?perPage=100&state=all&q="+url.QueryEscape(query), nil, 200)
		}
		in := "/repos/alice/" + I + "/issues"
		if ns := numbers(q("carol", in, "crash")); !has(ns, n1) || has(ns, n2) {
			t.Errorf("testo libero: %v", ns)
		}
		if ns := numbers(q("carol", in, "is:closed reason:not-planned")); len(ns) != 1 || !has(ns, n3) {
			t.Errorf("is:closed reason:not-planned: %v", ns)
		}
		if ns := numbers(q("carol", in, "label:area-rete")); len(ns) != 1 || !has(ns, n1) {
			t.Errorf("label: %v", ns)
		}
		if ns := numbers(q("carol", in, "assignee:botty")); len(ns) != 1 || !has(ns, n1) {
			t.Errorf("assignee: %v", ns)
		}
		if ns := numbers(q("carol", in, "assignee:@agents")); len(ns) != 1 || !has(ns, n1) {
			t.Errorf("assignee:@agents: %v", ns)
		}
		if ns := numbers(q("carol", in, "milestone:v1")); len(ns) != 1 || !has(ns, n1) {
			t.Errorf("milestone: %v", ns)
		}
		if ns := numbers(q("carol", in, "author:carol")); !has(ns, n1) || has(ns, n2) {
			t.Errorf("author: %v", ns)
		}
		if ns := numbers(q("carol", in, "is:open no:label no:assignee")); has(ns, n1) || !has(ns, n2) {
			t.Errorf("no:: %v", ns)
		}
		// sintassi che non si interpreta: 422
		e.deny("carol", "GET", in+"?q="+url.QueryEscape("is:sconosciuto"), nil, 422, "")
		// globale: stessa sintassi, con repo: e org:
		if ns := numbers(q("carol", "/search/issues", "repo:alice/"+I+" crash")); !has(ns, n1) {
			t.Errorf("globale con repo:: %v", ns)
		}
		// org: restringe agli owner di tipo organizzazione (qui alice è un utente: nessun risultato)
		if ns := numbers(q("carol", "/search/issues", "org:alice crash")); len(ns) != 0 {
			t.Errorf("globale con org: di un utente: %v", ns)
		}

		// nessuna fuga dal repo privato: né il testo, né l'etichetta, né i conteggi
		for _, query := range []string{"ZAFFIRO", "CHIAVE-ARANCIO", "ZAFFIRO-RISERVATO-91", "label:" + privLabel, "repo:alice/" + P, "repo:alice/" + P + " ZAFFIRO", "org:alice ZAFFIRO", "migrazione"} {
			r := e.api("carol", "GET", "/search/issues?perPage=100&state=all&q="+url.QueryEscape(query), nil)
			if r.status != 200 && r.status != 404 && r.status != 422 {
				t.Errorf("carol cerca %q: %d", query, r.status)
			}
			e.issueLeak("ricerca globale carol "+query, string(r.body))
			if r.status == 200 {
				if tot, _ := r.json()["total"].(float64); tot != 0 {
					t.Errorf("carol cerca %q: total %v, voluto 0: %s", query, tot, truncate(r.body))
				}
			}
		}
		// l'elenco globale senza filtri non nomina il repo privato a carol, ma sì a dave e alice
		all := e.must("carol", "GET", "/search/issues?perPage=100&state=all", nil, 200)
		e.issueLeak("ricerca globale senza filtri carol", string(all.body))
		for _, u := range []string{"dave", "alice"} {
			r := q(u, "/search/issues", "ZAFFIRO")
			if !strings.Contains(string(r.body), e2ePrivate) {
				t.Errorf("%s non trova la issue del repo privato: %s", u, r.body)
			}
		}
		if r := q("bob", "/search/issues", "ZAFFIRO"); !strings.Contains(string(r.body), "ZAFFIRO") {
			t.Errorf("bob (write) non trova la issue: %s", r.body)
		}
		// il testo malizioso resta testo (nessun errore SQL né 5xx)
		for _, query := range []string{"'; DROP TABLE core.issues; --", "label:\"a\" OR 1=1", `"` + strings.Repeat("a", 5000)} {
			if r := e.api("carol", "GET", "/search/issues?q="+url.QueryEscape(query), nil); r.status >= 500 {
				t.Errorf("q %.20q: %d", query, r.status)
			}
		}
		e.must("carol", "GET", issuePath(I, n1, ""), nil, 200)
	})

	// --- R10: repo archiviato in sola lettura ------------------------------------
	t.Run("repo_archiviato_R10", func(t *testing.T) {
		n := e.open("bob", I, "Aperta prima dell'archiviazione", "", nil)
		e.must("alice", "PATCH", "/repos/alice/"+I, map[string]any{"archived": true}, 200)
		defer e.must("alice", "PATCH", "/repos/alice/"+I, map[string]any{"archived": false}, 200)
		// si legge tutto
		e.must("carol", "GET", issuePath(I, n, ""), nil, 200)
		e.must("carol", "GET", "/repos/alice/"+I+"/issues", nil, 200)
		e.must("carol", "GET", issuePath(I, n, "/comments"), nil, 200)
		// non si scrive
		e.deny("carol", "POST", "/repos/alice/"+I+"/issues", map[string]any{"title": "x"}, 409, "archived")
		e.deny("bob", "POST", issuePath(I, n, "/comments"), map[string]any{"body": "x"}, 409, "archived")
		e.deny("bob", "POST", issuePath(I, n, "/close"), map[string]any{}, 409, "archived")
		e.deny("bob", "PUT", issuePath(I, n, "/labels"), map[string]any{"labels": []string{"bug"}}, 409, "archived")
		e.deny("bob", "PUT", issuePath(I, n, "/assignees"), map[string]any{"assignees": []string{"bob"}}, 409, "archived")
		e.deny("bob", "PUT", issuePath(I, n, "/milestone"), map[string]any{"milestone": 1}, 409, "archived")
		e.deny("bob", "PATCH", issuePath(I, n, ""), map[string]any{"title": "x"}, 409, "archived")
		e.deny("alice", "PUT", issuePath(I, n, "/lock"), map[string]any{}, 409, "archived")
		e.deny("alice", "PUT", issuePath(I, n, "/hidden"), map[string]any{"hidden": true}, 409, "archived")
		if r := e.upload("bob", I, "a.png", pngTiny); r.status != 409 {
			t.Errorf("upload su archiviato: %d, voluto 409", r.status)
		}
		if r := e.must("carol", "GET", issuePath(I, n, ""), nil, 200); r.json()["state"] != "open" || r.json()["title"] != "Aperta prima dell'archiviazione" {
			t.Errorf("la issue è cambiata: %s", r.body)
		}
	})
	e.must("bob", "POST", "/repos/alice/"+I+"/issues", map[string]any{"title": "dopo la riattivazione"}, 201)

	t.Run("ui_smoke", func(t *testing.T) {
		e.issuesUISmoke(t, I, int(n1))
	})
}

// issuesUISmoke lancia il controllo di fumo della UI sulle pagine 12 (lista
// delle issues), 13 (dettaglio) e 20 (nuova issue): web/src/smoke/issues.smoke.test.tsx,
// Vitest con le pagine vere di App.tsx contro il gateway vero. Senza corepack o
// node_modules è saltato, a meno di GITSTACK_REQUIRE_UI_SMOKE.
func (e *issuesE2E) issuesUISmoke(t *testing.T, repo string, issue int) {
	skip := func(why string) {
		if os.Getenv("GITSTACK_REQUIRE_UI_SMOKE") != "" {
			t.Fatalf("smoke UI richiesto ma non eseguibile: %s", why)
		}
		t.Skip(why)
	}
	web, err := filepath.Abs("../../../../web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("corepack"); err != nil {
		skip("corepack non è nel PATH")
	}
	if _, err := os.Stat(filepath.Join(web, "node_modules")); err != nil {
		skip("web/node_modules manca: corepack pnpm install --frozen-lockfile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "corepack", "pnpm", "exec", "vitest", "run", "src/smoke/issues.smoke.test.tsx")
	cmd.Dir = web
	cmd.Env = append(os.Environ(),
		"VITE_SMOKE_GATEWAY="+e.gateway,
		"VITE_SMOKE_TOKEN="+e.tokens["alice"],
		"VITE_SMOKE_ISSUES_REPO=alice/"+repo,
		fmt.Sprintf("VITE_SMOKE_ISSUE=%d", issue),
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke UI: %v\n%s", err, out)
	}
	plain := ansiRe.ReplaceAllString(string(out), "")
	if strings.Contains(plain, "skipped") || strings.Contains(plain, "failed") || !strings.Contains(plain, "passed") {
		t.Fatalf("smoke UI: test saltati, falliti o non eseguiti:\n%s", out)
	}
	t.Logf("smoke UI:\n%s", out)
}
