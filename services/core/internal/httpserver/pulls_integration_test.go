//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// GIT-261 (v1.1): Pull Request, regole PR1 e PR6. Il servizio git è finto
// (branch e confronto con risposte fisse): qui si provano core, permessi,
// numerazione condivisa con le issues (I1) e le transizioni di stato.

const mainSha = "1111111111111111111111111111111111111111"

func sha(n int) string { return fmt.Sprintf("%040x", n+0x2000) }

// prGit aggiunge a fakeGit branch e confronto (gitclient.Reader).
type prGit struct {
	*fakeGit
	bmu sync.Mutex
	br  map[uuid.UUID]map[string]string
}

func (g *prGit) of(id uuid.UUID) map[string]string {
	if g.br[id] == nil {
		g.br[id] = map[string]string{"main": mainSha}
	}
	return g.br[id]
}

func (g *prGit) setBranch(id uuid.UUID, name, s string) {
	g.bmu.Lock()
	defer g.bmu.Unlock()
	g.of(id)[name] = s
}

func (g *prGit) dropBranch(id uuid.UUID, name string) {
	g.bmu.Lock()
	defer g.bmu.Unlock()
	delete(g.of(id), name)
}

func (g *prGit) has(id uuid.UUID, name string) bool {
	g.bmu.Lock()
	defer g.bmu.Unlock()
	_, ok := g.of(id)[name]
	return ok
}

func (g *prGit) ReadJSON(_ context.Context, _ trust.Identity, repo uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	g.bmu.Lock()
	defer g.bmu.Unlock()
	br := g.of(repo)
	switch path {
	case "branches":
		var items []string
		names := make([]string, 0, len(br))
		for n := range br {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			commit := strings.Replace(commitJSON, "0123456789abcdef0123456789abcdef01234567", br[n], 1)
			items = append(items, fmt.Sprintf(`{"name":%q,"isDefault":%t,"protected":false,"commit":%s}`, n, n == "main", commit))
		}
		return json.RawMessage(fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(items, ","), len(items))), nil
	case "compare":
		resolve := func(ref string) (string, bool) {
			if s, ok := br[ref]; ok {
				return s, true
			}
			if len(ref) == 40 {
				return ref, true
			}
			return "", false
		}
		b, ok1 := resolve(q.Get("base"))
		h, ok2 := resolve(q.Get("head"))
		if !ok1 || !ok2 {
			return nil, &gitclient.APIError{Status: 404, Code: "ref_not_found", Message: "ref non trovato"}
		}
		ahead := 2
		if b == h {
			ahead = 0
		}
		commits := make([]string, ahead)
		for i := range commits {
			commits[i] = strings.Replace(commitJSON, "0123456789abcdef0123456789abcdef01234567", sha(100+i), 1)
		}
		return json.RawMessage(fmt.Sprintf(`{"base":%q,"head":%q,"baseSha":%q,"headSha":%q,"mergeBase":%q,"aheadBy":%d,"behindBy":0,
			"commits":[%s],"commitsTruncated":false,
			"files":[{"path":"a.txt","status":"modified","additions":3,"deletions":1,"binary":false,"truncated":false},{"path":"b.txt","status":"added","additions":2,"deletions":0,"binary":false,"truncated":false}],
			"filesChanged":2,"additions":5,"deletions":1,"truncated":false,"listOnly":false}`,
			q.Get("base"), q.Get("head"), b, h, mainSha, ahead, strings.Join(commits, ","))), nil
	}
	return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "non trovato"}
}

func (g *prGit) OpenStream(context.Context, trust.Identity, uuid.UUID, string, url.Values) (*gitclient.Stream, error) {
	return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "non trovato"}
}

type pullsEnv struct {
	*issuesEnv
	git *prGit
}

func newPullsEnv(t *testing.T) *pullsEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	fg := newFakeGit()
	g := &prGit{fakeGit: fg, br: map[uuid.UUID]map[string]string{}}
	e := &issuesEnv{t: t, pool: pool, id: &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(g),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return &pullsEnv{issuesEnv: e, git: g}
}

func (e *pullsEnv) pr(rec *httptest.ResponseRecorder) openapi.PullRequest {
	e.t.Helper()
	var x openapi.PullRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &x); err != nil {
		e.t.Fatalf("PR non valida: %v: %s", err, rec.Body.String())
	}
	return x
}

// branch crea un branch con commit di differenza rispetto a main.
func (e *pullsEnv) branch(repo uuid.UUID, name string) {
	e.git.setBranch(repo, name, sha(len(name)*7+int(name[len(name)-1])))
}

// open apre una PR come user e ne ritorna il numero.
func (e *pullsEnv) open(repo, user, source string) int64 {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/pulls", user, fmt.Sprintf(`{"title":"PR %s","sourceBranch":%q}`, source, source))
	e.want(rec, http.StatusCreated, "")
	return e.pr(rec).Number
}

func (e *pullsEnv) prEvents(repo string, n int64) []string {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/%s/pulls/%d/events", repo, n), "alice", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestEventList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, ev := range l.Items {
		out = append(out, string(ev.Type))
	}
	return out
}

func TestPulls_CreazionePR1ePermessi(t *testing.T) {
	e := newPullsEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	secretID := e.repo("secret", false)
	e.branch(repoID, "feat")
	e.branch(secretID, "feat")
	e.git.setBranch(repoID, "same", mainSha)

	t.Run("read_non_apre_e_riceve_il_suggerimento_della_issue", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "carol", `{"title":"x","sourceBranch":"feat"}`)
		e.want(rec, http.StatusForbidden, "forbidden")
		if !strings.Contains(rec.Body.String(), "issue") {
			t.Fatalf("manca il suggerimento della issue: %s", rec.Body.String())
		}
	})

	t.Run("write_apre_e_la_risposta_e_completa", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"  Prima PR ","body":"**testo** Markdown","sourceBranch":"feat","reviewers":["carol"]}`)
		e.want(rec, http.StatusCreated, "")
		x := e.pr(rec)
		if x.Number != 1 || x.Title != "Prima PR" || x.Body != "**testo** Markdown" || x.State != "open" || x.Draft ||
			x.Author.Username != "bob" || x.SourceBranch != "feat" || x.TargetBranch != "main" || !x.SourceBranchExists ||
			x.CommitCount != 2 || x.ChangedFiles != 2 || x.Additions != 5 || x.Deletions != 1 || x.ViaToken != nil ||
			len(x.RequestedReviewers) != 1 || x.RequestedReviewers[0].Username != "carol" || x.ReviewDecision != "required" {
			t.Fatalf("PR inattesa: %+v", x)
		}
		if loc := rec.Header().Get("Location"); loc != "/repos/alice/app/pulls/1" {
			t.Fatalf("Location = %q", loc)
		}
		if got := e.prEvents("app", 1); !slices.Equal(got, []string{"opened"}) {
			t.Fatalf("cronologia = %v", got)
		}
	})

	t.Run("admin_apre_una_bozza_senza_revisori", func(t *testing.T) {
		e.branch(repoID, "draft-branch")
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "alice", `{"title":"Bozza","sourceBranch":"draft-branch","draft":true,"reviewers":["carol"]}`)
		e.want(rec, http.StatusCreated, "")
		x := e.pr(rec)
		if !x.Draft || x.State != "open" || len(x.RequestedReviewers) != 0 || x.Number != 2 {
			t.Fatalf("bozza inattesa: %+v", x)
		}
	})

	t.Run("validazione_422", func(t *testing.T) {
		for name, body := range map[string]string{
			"titolo_vuoto":         `{"title":"  ","sourceBranch":"feat"}`,
			"stesso_branch":        `{"title":"x","sourceBranch":"main"}`,
			"origine_inesistente":  `{"title":"x","sourceBranch":"nope"}`,
			"destinazione_manca":   `{"title":"x","sourceBranch":"feat","targetBranch":"nope"}`,
			"senza_commit":         `{"title":"x","sourceBranch":"same"}`,
			"revisore_sconosciuto": `{"title":"x","sourceBranch":"same2","reviewers":["nessuno"]}`,
		} {
			e.want(e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", body), http.StatusUnprocessableEntity, "validation_failed")
			_ = name
		}
		e.want(e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"x"}`), http.StatusBadRequest, "")
		e.want(e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"x","sourceBranch":"feat","extra":1}`), http.StatusBadRequest, "")
		long := strings.Repeat("a", 65537)
		e.want(e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", fmt.Sprintf(`{"title":"x","sourceBranch":"feat","body":%q}`, long)), http.StatusRequestEntityTooLarge, "body_too_large")
	})

	t.Run("revisore_senza_read_422", func(t *testing.T) {
		e.branch(repoID, "r2")
		e.repo("priv", false)
		// carol non legge "priv", ma su "app" sì: il caso è un repo privato.
		pid := e.repo("priv2", false)
		e.id.grantWrite(pid, "bob")
		e.branch(pid, "r2")
		rec := e.do(http.MethodPost, "/repos/alice/priv2/pulls", "bob", `{"title":"x","sourceBranch":"r2","reviewers":["carol"]}`)
		e.want(rec, http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("una_sola_PR_aperta_per_coppia_di_branch", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"doppia","sourceBranch":"feat"}`)
		e.want(rec, http.StatusConflict, "already_exists")
		// Il numero non è stato consumato.
		e.branch(repoID, "next")
		if n := e.open("app", "bob", "next"); n != 3 {
			t.Fatalf("numero = %d, voluto 3", n)
		}
	})

	t.Run("repo_non_leggibile_404_su_ogni_operazione", func(t *testing.T) {
		n := e.open("secret", "alice", "feat")
		base := fmt.Sprintf("/repos/alice/secret/pulls/%d", n)
		e.want(e.do(http.MethodPost, "/repos/alice/secret/pulls", "carol", `{"title":"x","sourceBranch":"feat"}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, "/repos/alice/secret/pulls", "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, "/repos/alice/inesistente/pulls", "alice", ""), http.StatusNotFound, "not_found")
		for _, c := range []struct{ m, p, b string }{
			{http.MethodGet, "", ""}, {http.MethodPatch, "", `{"title":"y"}`}, {http.MethodPost, "/close", ""},
			{http.MethodPost, "/reopen", ""}, {http.MethodPost, "/ready", ""}, {http.MethodPost, "/draft", ""},
			{http.MethodGet, "/events", ""}, {http.MethodGet, "/commits", ""}, {http.MethodGet, "/files", ""},
		} {
			e.want(e.do(c.m, base+c.p, "carol", c.b), http.StatusNotFound, "not_found")
		}
	})

	t.Run("repo_archiviato_409", func(t *testing.T) {
		aid := e.repo("old", true)
		e.id.grantWrite(aid, "bob")
		e.branch(aid, "feat")
		n := e.open("old", "bob", "feat")
		e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, aid)
		e.want(e.do(http.MethodPost, "/repos/alice/old/pulls", "bob", `{"title":"x","sourceBranch":"feat"}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/old/pulls/%d/close", n), "bob", ""), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPatch, fmt.Sprintf("/repos/alice/old/pulls/%d", n), "bob", `{"title":"y"}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/old/pulls/%d", n), "carol", ""), http.StatusOK, "")
	})

	t.Run("mirror_in_pull_sola_lettura", func(t *testing.T) {
		mid := e.repo("mirrored", true)
		e.id.grantWrite(mid, "bob")
		e.branch(mid, "feat")
		e.sql(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, state, created_by) VALUES ($1, $2, 'https://example.com/x.git', 'in_sync', $3)`, uuid.New(), mid, uuid.MustParse(aliceID))
		e.want(e.do(http.MethodPost, "/repos/alice/mirrored/pulls", "bob", `{"title":"x","sourceBranch":"feat"}`), http.StatusConflict, "read_only_mirror")
	})
}

func TestPulls_TokenConScope_ViaToken(t *testing.T) {
	e := newPullsEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	tokenID := uuid.New()
	rec := e.doWithToken(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"da agente","sourceBranch":"feat"}`, tokenID, "ci-runner")
	e.want(rec, http.StatusCreated, "")
	x := e.pr(rec)
	if x.ViaToken == nil || x.ViaToken.Name != "ci-runner" || uuid.UUID(x.ViaToken.Id) != tokenID {
		t.Fatalf("viaToken = %+v", x.ViaToken)
	}
	again := e.pr(e.do(http.MethodGet, "/repos/alice/app/pulls/1", "carol", ""))
	if again.ViaToken == nil || again.ViaToken.Name != "ci-runner" {
		t.Fatalf("viaToken in lettura = %+v", again.ViaToken)
	}
	// Un token di bob non dà più di quanto bob può: su un repo che non
	// legge, 404; con write ma senza write su un altro repo, 403.
	e.repo("secret", false)
	rec = e.doWithToken(http.MethodPost, "/repos/alice/secret/pulls", "bob", `{"title":"x","sourceBranch":"feat"}`, tokenID, "ci-runner")
	e.want(rec, http.StatusNotFound, "not_found")
	rec = e.doWithToken(http.MethodPost, "/repos/alice/app/pulls", "carol", `{"title":"x","sourceBranch":"feat"}`, tokenID, "ci-runner")
	e.want(rec, http.StatusForbidden, "forbidden")
}

func TestPulls_NumeroCondivisoConLeIssuesI1(t *testing.T) {
	e := newPullsEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")

	t.Run("sequenza_mista", func(t *testing.T) {
		e.branch(repoID, "a")
		e.branch(repoID, "b")
		if n := e.open("app", "bob", "a"); n != 1 {
			t.Fatalf("PR = #%d", n)
		}
		if n := e.issuesEnv.open("app", "carol", "issue"); n != 2 {
			t.Fatalf("issue = #%d", n)
		}
		if n := e.open("app", "bob", "b"); n != 3 {
			t.Fatalf("PR = #%d", n)
		}
		// Un numero di issue non è una PR, e viceversa.
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls/2", "alice", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPost, "/repos/alice/app/pulls/2/close", "alice", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues/1", "alice", ""), http.StatusNotFound, "")
	})

	t.Run("creazioni_concorrenti_numeri_unici_e_contigui", func(t *testing.T) {
		e.repo("race", true)
		rid := e.git.idOf(t, e, "race")
		e.id.grantWrite(rid, "bob")
		const n = 12
		for i := 0; i < n; i++ {
			e.branch(rid, fmt.Sprintf("br%02d", i))
		}
		var wg sync.WaitGroup
		numbers := make(chan int64, 2*n)
		errs := make(chan string, 2*n)
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				rec := e.do(http.MethodPost, "/repos/alice/race/pulls", "bob", fmt.Sprintf(`{"title":"p%d","sourceBranch":"br%02d"}`, i, i))
				if rec.Code != http.StatusCreated {
					errs <- fmt.Sprintf("PR %d %s", rec.Code, rec.Body.String())
					return
				}
				var x openapi.PullRequest
				if err := json.Unmarshal(rec.Body.Bytes(), &x); err != nil {
					errs <- err.Error()
					return
				}
				numbers <- x.Number
			}(i)
			go func(i int) {
				defer wg.Done()
				rec := e.do(http.MethodPost, "/repos/alice/race/issues", "bob", fmt.Sprintf(`{"title":"i%d"}`, i))
				if rec.Code != http.StatusCreated {
					errs <- fmt.Sprintf("issue %d %s", rec.Code, rec.Body.String())
					return
				}
				var x openapi.Issue
				if err := json.Unmarshal(rec.Body.Bytes(), &x); err != nil {
					errs <- err.Error()
					return
				}
				numbers <- x.Number
			}(i)
		}
		wg.Wait()
		close(numbers)
		close(errs)
		for m := range errs {
			t.Error(m)
		}
		var got []int64
		for v := range numbers {
			got = append(got, v)
		}
		slices.Sort(got)
		if len(got) != 2*n {
			t.Fatalf("numeri = %d, voluti %d", len(got), 2*n)
		}
		for i, v := range got {
			if v != int64(i+1) {
				t.Fatalf("numeri non contigui o duplicati: %v", got)
			}
		}
	})

	t.Run("stessa_coppia_in_concorrenza_una_sola_PR", func(t *testing.T) {
		e.repo("dup", true)
		rid := e.git.idOf(t, e, "dup")
		e.id.grantWrite(rid, "bob")
		e.branch(rid, "same-pair")
		var wg sync.WaitGroup
		codes := make(chan int, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes <- e.do(http.MethodPost, "/repos/alice/dup/pulls", "bob", `{"title":"x","sourceBranch":"same-pair"}`).Code
			}()
		}
		wg.Wait()
		close(codes)
		created := 0
		for c := range codes {
			switch c {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
			default:
				t.Errorf("codice %d", c)
			}
		}
		if created != 1 {
			t.Fatalf("PR create = %d, voluta 1", created)
		}
		// Nessun numero sprecato: la prossima issue è la #2.
		if n := e.issuesEnv.open("dup", "bob", "dopo"); n != 2 {
			t.Fatalf("issue = #%d, voluto 2 (nessun numero consumato dai conflitti)", n)
		}
	})
}

// idOf trova l'id di un repo di alice per nome.
func (g *prGit) idOf(t *testing.T, e *pullsEnv, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT resource_id FROM core.repositories WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPulls_BozzaChiusuraERiapertura_PR6(t *testing.T) {
	e := newPullsEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	e.branch(repoID, "other")
	n := e.open("app", "bob", "feat")
	path := fmt.Sprintf("/repos/alice/app/pulls/%d", n)

	t.Run("bozza_e_ready_for_review", func(t *testing.T) {
		e.want(e.do(http.MethodPost, path+"/ready", "bob", ""), http.StatusConflict, "not_draft")
		x := e.pr(e.do(http.MethodPost, path+"/draft", "bob", ""))
		if !x.Draft {
			t.Fatal("doveva essere una bozza")
		}
		e.want(e.do(http.MethodPost, path+"/draft", "bob", ""), http.StatusConflict, "already_draft")
		e.want(e.do(http.MethodPost, path+"/ready", "carol", ""), http.StatusForbidden, "forbidden")
		x = e.pr(e.do(http.MethodPost, path+"/ready", "alice", ""))
		if x.Draft || x.State != "open" {
			t.Fatalf("PR inattesa: %+v", x)
		}
	})

	t.Run("modifica_titolo_testo_e_destinazione", func(t *testing.T) {
		e.want(e.do(http.MethodPatch, path, "carol", `{"title":"hack"}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPatch, path, "bob", `{}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPatch, path, "bob", `{"targetBranch":"feat"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPatch, path, "bob", `{"targetBranch":"nope"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPatch, path, "bob", `{"title":""}`), http.StatusUnprocessableEntity, "validation_failed")
		x := e.pr(e.do(http.MethodPatch, path, "alice", `{"title":"Nuovo titolo","body":"nuovo **testo**","targetBranch":"other"}`))
		if x.Title != "Nuovo titolo" || x.Body != "nuovo **testo**" || x.TargetBranch != "other" {
			t.Fatalf("PR inattesa: %+v", x)
		}
		x = e.pr(e.do(http.MethodPatch, path, "bob", `{"targetBranch":"main"}`))
		if x.TargetBranch != "main" {
			t.Fatalf("destinazione = %q", x.TargetBranch)
		}
	})

	t.Run("chiudere_non_elimina_il_branch_e_riaprire_lo_richiede", func(t *testing.T) {
		x := e.pr(e.do(http.MethodPost, path+"/close", "bob", ""))
		if x.State != "closed" || x.ClosedAt == nil || x.Draft {
			t.Fatalf("PR chiusa inattesa: %+v", x)
		}
		if !e.git.has(repoID, "feat") || !x.SourceBranchExists {
			t.Fatal("chiudere non deve eliminare il branch")
		}
		e.want(e.do(http.MethodPost, path+"/close", "bob", ""), http.StatusConflict, "already_closed")
		e.want(e.do(http.MethodPatch, path, "bob", `{"title":"x"}`), http.StatusConflict, "not_open")
		e.want(e.do(http.MethodPost, path+"/ready", "bob", ""), http.StatusConflict, "not_open")
		e.want(e.do(http.MethodPost, path+"/draft", "bob", ""), http.StatusConflict, "not_open")

		// Il branch di origine sparisce: la riapertura fallisce, la PR resta chiusa.
		e.git.dropBranch(repoID, "feat")
		rec := e.do(http.MethodPost, path+"/reopen", "bob", "")
		e.want(rec, http.StatusConflict, "branch_missing")
		gone := e.pr(e.do(http.MethodGet, path, "carol", ""))
		if gone.State != "closed" || gone.SourceBranchExists {
			t.Fatalf("PR dopo la riapertura fallita: %+v", gone)
		}
		// Il diff congelato resta leggibile dagli sha salvati.
		e.want(e.do(http.MethodGet, path+"/files", "carol", ""), http.StatusOK, "")

		// Anche la destinazione deve esistere.
		e.branch(repoID, "feat")
		e.git.dropBranch(repoID, "main")
		e.want(e.do(http.MethodPost, path+"/reopen", "bob", ""), http.StatusConflict, "branch_missing")
		e.git.setBranch(repoID, "main", mainSha)

		e.want(e.do(http.MethodPost, path+"/reopen", "carol", ""), http.StatusForbidden, "forbidden")
		x = e.pr(e.do(http.MethodPost, path+"/reopen", "bob", ""))
		if x.State != "open" || x.ClosedAt != nil {
			t.Fatalf("PR riaperta inattesa: %+v", x)
		}
		e.want(e.do(http.MethodPost, path+"/reopen", "bob", ""), http.StatusConflict, "already_open")
	})

	t.Run("riaprire_con_una_PR_aperta_sulla_stessa_coppia_409", func(t *testing.T) {
		e.branch(repoID, "twin")
		a := e.open("app", "bob", "twin")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", a), "bob", ""), http.StatusOK, "")
		b := e.open("app", "bob", "twin")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/reopen", a), "bob", ""), http.StatusConflict, "already_exists")
		_ = b
	})

	t.Run("autore_con_solo_read_gestisce_la_propria", func(t *testing.T) {
		e.branch(repoID, "carols")
		m := e.open("app", "bob", "carols")
		e.sql(`UPDATE core.pull_requests SET author_id = $1 WHERE repo_id = $2 AND number = $3`, uuid.MustParse(carolID), repoID, m)
		p := fmt.Sprintf("/repos/alice/app/pulls/%d", m)
		e.want(e.do(http.MethodPatch, p, "carol", `{"title":"mia"}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, p+"/close", "carol", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPost, p+"/reopen", "carol", ""), http.StatusOK, "")
	})

	t.Run("una_PR_unita_e_definitiva", func(t *testing.T) {
		e.branch(repoID, "merged-one")
		m := e.open("app", "bob", "merged-one")
		e.sql(`UPDATE core.pull_requests SET state = 'merged', merge_method = 'merge', merged_by = $1, merged_at = now(), merge_commit_sha = $2
			WHERE repo_id = $3 AND number = $4`, uuid.MustParse(aliceID), sha(9), repoID, m)
		p := fmt.Sprintf("/repos/alice/app/pulls/%d", m)
		x := e.pr(e.do(http.MethodGet, p, "carol", ""))
		if x.State != "merged" || x.MergedBy == nil || x.MergedBy.Username != "alice" || x.MergeMethod == nil || *x.MergeMethod != "merge" {
			t.Fatalf("PR unita inattesa: %+v", x)
		}
		e.want(e.do(http.MethodPost, p+"/close", "alice", ""), http.StatusConflict, "already_merged")
		e.want(e.do(http.MethodPost, p+"/reopen", "alice", ""), http.StatusConflict, "already_merged")
		e.want(e.do(http.MethodPost, p+"/draft", "alice", ""), http.StatusConflict, "already_merged")
		e.want(e.do(http.MethodPatch, p, "alice", `{"title":"x"}`), http.StatusConflict, "not_open")
	})

	t.Run("cronologia", func(t *testing.T) {
		want := []string{"opened", "converted_to_draft", "ready_for_review", "renamed", "edited", "retargeted", "retargeted", "closed", "reopened"}
		if got := e.prEvents("app", n); !slices.Equal(got, want) {
			t.Fatalf("cronologia = %v\nvoluta     %v", got, want)
		}
	})
}

func TestPulls_ElencoDettaglioCommitEFile(t *testing.T) {
	e := newPullsEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	for _, b := range []string{"one", "two", "three"} {
		e.branch(repoID, b)
	}
	a := e.open("app", "bob", "one")
	e.want(e.do(http.MethodPost, "/repos/alice/app/pulls", "alice", `{"title":"Cerco-me","body":"con 100% di _prove_","sourceBranch":"two","draft":true}`), http.StatusCreated, "")
	c := e.open("app", "alice", "three")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", c), "alice", ""), http.StatusOK, "")

	list := func(q string) openapi.PullRequestList {
		rec := e.do(http.MethodGet, "/repos/alice/app/pulls"+q, "carol", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.PullRequestList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	numbers := func(l openapi.PullRequestList) []int64 {
		var out []int64
		for _, it := range l.Items {
			out = append(out, it.Number)
		}
		return out
	}

	t.Run("filtri", func(t *testing.T) {
		for q, want := range map[string][]int64{
			"":                                    {2, 1},
			"?state=closed":                       {3},
			"?state=all":                          {3, 2, 1},
			"?state=merged":                       nil,
			"?q=is:closed":                        {3},
			"?q=is:draft":                         {2},
			"?q=author:bob":                       {1},
			"?q=author:@me":                       nil,
			"?q=100%25":                           {2},
			"?q=cerco":                            {2},
			"?head=one":                           {1},
			"?base=main&sort=updated":             {2, 1},
			"?sort=comments":                      {2, 1},
			"?q=" + url.QueryEscape("is:open is:pr cerco"): {2},
		} {
			if got := numbers(list(q)); !slices.Equal(got, want) {
				t.Errorf("%q: %v, voluto %v", q, got, want)
			}
		}
		l := list("?state=all&perPage=2&page=2")
		if l.Total != 3 || l.Page != 2 || l.PerPage != 2 || len(l.Items) != 1 || l.Items[0].Number != 1 {
			t.Fatalf("pagina inattesa: %+v", l)
		}
		it := list("")
		if it.Items[1].Author.Username != "bob" || !it.Items[0].Draft || it.Items[0].ReviewDecision != "none" {
			t.Fatalf("riga inattesa: %+v", it.Items)
		}
	})

	t.Run("richieste_non_valide", func(t *testing.T) {
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls?q=review:approved", "carol", ""), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls?q=is:nope", "carol", ""), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls?page=0", "carol", ""), http.StatusBadRequest, "invalid_page")
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls?perPage=101", "carol", ""), http.StatusBadRequest, "invalid_per_page")
	})

	t.Run("commit_e_file", func(t *testing.T) {
		base := fmt.Sprintf("/repos/alice/app/pulls/%d", a)
		rec := e.do(http.MethodGet, base+"/commits?perPage=1", "carol", "")
		e.want(rec, http.StatusOK, "")
		var cl openapi.CommitList
		if err := json.Unmarshal(rec.Body.Bytes(), &cl); err != nil || len(cl.Items) != 1 || !cl.HasMore || cl.PerPage != 1 {
			t.Fatalf("commit inattesi: %v %+v", err, cl)
		}
		rec = e.do(http.MethodGet, base+"/commits?perPage=1&page=2", "carol", "")
		e.want(rec, http.StatusOK, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &cl); err != nil || len(cl.Items) != 1 || cl.HasMore {
			t.Fatalf("seconda pagina inattesa: %v %+v", err, cl)
		}
		e.want(e.do(http.MethodGet, base+"/commits?perPage=1000", "carol", ""), http.StatusBadRequest, "")

		rec = e.do(http.MethodGet, base+"/files", "carol", "")
		e.want(rec, http.StatusOK, "")
		var f openapi.PullRequestFiles
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || len(f.Files) != 2 || f.FilesChanged != 2 || f.Additions != 5 || f.ViewedPaths == nil || f.BaseSha != mainSha {
			t.Fatalf("file inattesi: %v %+v", err, f)
		}
		// File Viewed personali: valgono per chi li ha marcati, sull'head attuale.
		e.sql(`INSERT INTO core.pull_request_viewed_files (pull_request_id, user_id, path, viewed_sha)
			SELECT id, $1, 'a.txt', head_sha FROM core.pull_requests WHERE number = $2`, uuid.MustParse(carolID), a)
		rec = e.do(http.MethodGet, base+"/files?path=a.txt", "carol", "")
		e.want(rec, http.StatusOK, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || len(f.Files) != 1 || f.Files[0].Path != "a.txt" || !slices.Equal(f.ViewedPaths, []string{"a.txt"}) {
			t.Fatalf("file filtrati inattesi: %v %+v", err, f)
		}
		other := e.pr(e.do(http.MethodGet, base, "alice", ""))
		_ = other
		rec = e.do(http.MethodGet, base+"/files", "bob", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || len(f.ViewedPaths) != 0 {
			t.Fatalf("Viewed di un altro utente: %v %+v", err, f)
		}
		e.want(e.do(http.MethodGet, base+"/files?path=zzz", "carol", ""), http.StatusNotFound, "not_found")
	})

	t.Run("dettaglio_con_commenti_e_review_decision", func(t *testing.T) {
		x := e.pr(e.do(http.MethodGet, "/repos/alice/app/pulls/"+strconv.FormatInt(a, 10), "carol", ""))
		if x.Number != a || x.CommentCount != 0 || x.UnresolvedThreadCount != 0 || x.ReviewDecision != "none" || x.Approvals != 0 || x.RequiredApprovals != 0 || x.ClosingIssues == nil {
			t.Fatalf("dettaglio inatteso: %+v", x)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/app/pulls/999", "carol", ""), http.StatusNotFound, "not_found")
	})
}
