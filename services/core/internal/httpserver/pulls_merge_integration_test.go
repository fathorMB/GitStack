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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GIT-263 (v1.1): merge delle PR, regole PR3, PR7 e metodi/eliminazione del
// branch. Il servizio git è finto ma con la semantica che conta per core:
// merge con sha atteso della destinazione (compare-and-swap), conflitti,
// branch indietro, eliminazione con sha atteso.

// mergeGit aggiunge a prGit unibilità, merge ed eliminazione dei branch.
type mergeGit struct {
	*prGit
	mmu       sync.Mutex
	conflicts map[string][]string
	behind    map[string]int
	merges    []gitclient.MergeRequest
	deletes   []gitclient.DeleteBranchRequest
	// barrier: se non nil, ogni Merge aspetta che ne siano arrivati due prima
	// di confrontare lo sha atteso (fa correre davvero due merge insieme).
	barrier *sync.WaitGroup
}

func key(repo uuid.UUID, branch string) string { return repo.String() + "|" + branch }

func (g *mergeGit) ReadJSON(ctx context.Context, c trust.Identity, repo uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	g.mmu.Lock()
	files, conflicting := g.conflicts[key(repo, q.Get("head"))]
	behind := g.behind[key(repo, q.Get("head"))]
	g.mmu.Unlock()
	switch path {
	case "mergeability":
		st := "clean"
		switch {
		case conflicting:
			st = "conflicts"
		case behind > 0:
			st = "behind"
		}
		if files == nil {
			files = []string{}
		}
		return json.RawMessage(fmt.Sprintf(`{"base":%q,"head":%q,"state":%q,"mergeable":%t,"conflicts":%s}`,
			q.Get("base"), q.Get("head"), st, !conflicting, mustJSON(files))), nil
	case "compare":
		raw, err := g.prGit.ReadJSON(ctx, c, repo, path, q)
		if err != nil || behind == 0 {
			return raw, err
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		m["behindBy"] = behind
		return json.RawMessage(mustJSON(m)), nil
	}
	return g.prGit.ReadJSON(ctx, c, repo, path, q)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (g *mergeGit) Merge(_ context.Context, _ trust.Identity, repo uuid.UUID, in gitclient.MergeRequest) (gitclient.MergeResult, error) {
	if g.barrier != nil {
		g.barrier.Done()
		done := make(chan struct{})
		go func() { g.barrier.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	g.bmu.Lock()
	defer g.bmu.Unlock()
	br := g.of(repo)
	if br[in.Base] != in.ExpectedBaseSha {
		return gitclient.MergeResult{}, &gitclient.MergeError{Status: 409, Code: "stale", Ref: in.Base, CurrentSha: br[in.Base]}
	}
	if in.ExpectedHeadSha != "" && br[in.Head] != in.ExpectedHeadSha {
		return gitclient.MergeResult{}, &gitclient.MergeError{Status: 409, Code: "stale", Ref: in.Head, CurrentSha: br[in.Head]}
	}
	g.mmu.Lock()
	g.merges = append(g.merges, in)
	n := len(g.merges)
	g.mmu.Unlock()
	res := gitclient.MergeResult{Sha: sha(900 + n), Method: in.Method, Parents: []string{br[in.Base]},
		BaseSha: br[in.Base], HeadSha: br[in.Head], MergeBase: mainSha}
	br[in.Base] = res.Sha
	return res, nil
}

func (g *mergeGit) DeleteBranch(_ context.Context, _ trust.Identity, repo uuid.UUID, in gitclient.DeleteBranchRequest) error {
	g.bmu.Lock()
	defer g.bmu.Unlock()
	br := g.of(repo)
	cur, ok := br[in.Branch]
	switch {
	case !ok:
		return &gitclient.MergeError{Status: 404, Code: "ref_not_found", Message: "branch non trovato"}
	case in.Branch == "main":
		return &gitclient.MergeError{Status: 409, Code: "default_branch"}
	case in.ExpectedSha != "" && in.ExpectedSha != cur:
		return &gitclient.MergeError{Status: 409, Code: "stale", Ref: in.Branch, CurrentSha: cur}
	}
	delete(br, in.Branch)
	g.mmu.Lock()
	g.deletes = append(g.deletes, in)
	g.mmu.Unlock()
	return nil
}

func (g *mergeGit) lastMerge() gitclient.MergeRequest {
	g.mmu.Lock()
	defer g.mmu.Unlock()
	return g.merges[len(g.merges)-1]
}

func (g *mergeGit) mergeCount() int {
	g.mmu.Lock()
	defer g.mmu.Unlock()
	return len(g.merges)
}

type mergeEnv struct {
	*pullsEnv
	mg *mergeGit
}

func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	return newMergeEnvMaxConns(t, 0)
}

// newMergeEnvMaxConns: con maxConns > 0 il router usa un pool separato di
// quella dimensione (le fixture restano sul pool grande), per riprodurre i
// deadlock da esaurimento del pool.
func newMergeEnvMaxConns(t *testing.T, maxConns int32) *mergeEnv {
	t.Helper()
	pool, dsn := dbtest.NewPool(t)
	fixtures := pool
	if maxConns > 0 {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.MaxConns = maxConns
		small, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		// Close aspetta le connessioni in uso: con un deadlock non tornerebbe mai e il
		// test resterebbe appeso invece di fallire. Il container lo ferma comunque.
		t.Cleanup(func() { go small.Close() })
		pool = small
	}
	pg := &prGit{fakeGit: newFakeGit(), br: map[uuid.UUID]map[string]string{}}
	mg := &mergeGit{prGit: pg, conflicts: map[string][]string{}, behind: map[string]int{}}
	e := &issuesEnv{t: t, pool: fixtures, id: &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(mg),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return &mergeEnv{pullsEnv: &pullsEnv{issuesEnv: e, git: pg}, mg: mg}
}

func (e *mergeEnv) merge(repo string, n int64, user, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, fmt.Sprintf("/repos/alice/%s/pulls/%d/merge", repo, n), user, body)
}

func (e *mergeEnv) mergeability(repo string, n int64) openapi.PullRequestMergeability {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/%s/pulls/%d/mergeability", repo, n), "carol", "")
	e.want(rec, http.StatusOK, "")
	var m openapi.PullRequestMergeability
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *mergeEnv) settings(repo, user, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPatch, "/repos/alice/"+repo+"/branch-settings", user, body)
}

func TestPullsMerge_MergeCommitESquash(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	e.branch(repoID, "fix")
	n1 := e.open("app", "bob", "feat")
	n2 := e.open("app", "bob", "fix")

	t.Run("senza_write_403_e_metodo_sconosciuto_422", func(t *testing.T) {
		e.want(e.merge("app", n1, "carol", `{"method":"squash"}`), http.StatusForbidden, "forbidden")
		e.want(e.merge("app", n1, "bob", `{"method":"rebase"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.merge("app", 99, "bob", `{"method":"squash"}`), http.StatusNotFound, "not_found")
		if e.mg.mergeCount() != 0 {
			t.Fatal("nessun merge doveva partire")
		}
	})

	t.Run("mergeability_di_una_PR_pulita", func(t *testing.T) {
		m := e.mergeability("app", n1)
		if m.State != "mergeable" || !m.Mergeable || len(m.Blockers) != 0 || len(m.ConflictFiles) != 0 ||
			m.DefaultMethod != "squash" || !slices.Equal(m.AllowedMethods, []openapi.MergeMethod{"merge", "squash"}) {
			t.Fatalf("mergeability inattesa: %+v", m)
		}
	})

	t.Run("squash_con_titolo_proposto", func(t *testing.T) {
		rec := e.merge("app", n1, "bob", `{"method":"squash"}`)
		e.want(rec, http.StatusOK, "")
		x := e.pr(rec)
		if x.State != "merged" || x.MergeMethod == nil || *x.MergeMethod != "squash" || x.MergeCommitSha == nil || *x.MergeCommitSha == "" ||
			x.MergedBy == nil || x.MergedBy.Username != "bob" || x.MergedAt == nil {
			t.Fatalf("PR unita inattesa: %+v", x)
		}
		in := e.mg.lastMerge()
		if in.Method != "squash" || in.Base != "main" || in.Head != "feat" || !strings.HasPrefix(in.Message, "PR feat (#1)") ||
			in.Author.Name != "bob" || in.Pusher.Username != "bob" || in.Owner != "alice" || in.ExpectedBaseSha != mainSha {
			t.Fatalf("richiesta a git inattesa: %+v", in)
		}
		if got := e.prEvents("app", n1); !slices.Equal(got, []string{"opened", "merged"}) {
			t.Fatalf("cronologia = %v", got)
		}
		// Unita è definitiva: né un secondo merge né chiusura o riapertura.
		e.want(e.merge("app", n1, "bob", `{"method":"squash"}`), http.StatusConflict, "already_merged")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/reopen", n1), "bob", ""), http.StatusConflict, "already_merged")
	})

	t.Run("merge_commit_con_titolo_e_messaggio_scelti", func(t *testing.T) {
		rec := e.merge("app", n2, "alice", `{"method":"merge","commitTitle":"Unisco fix","commitMessage":"dettagli"}`)
		e.want(rec, http.StatusOK, "")
		x := e.pr(rec)
		if x.State != "merged" || *x.MergeMethod != "merge" {
			t.Fatalf("PR unita inattesa: %+v", x)
		}
		in := e.mg.lastMerge()
		if in.Method != "merge" || in.Message != "Unisco fix\n\ndettagli" {
			t.Fatalf("richiesta a git inattesa: %+v", in)
		}
		// La seconda PR è partita dalla punta di main spostata dalla prima.
		if in.ExpectedBaseSha == mainSha {
			t.Fatal("lo sha atteso doveva essere la punta dopo il primo merge")
		}
	})

	t.Run("agente_con_token_e_write_unisce", func(t *testing.T) {
		e.branch(repoID, "by-bot")
		n := e.open("app", "bob", "by-bot")
		rec := e.doWithToken(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/merge", n), "bob", `{"method":"squash"}`, uuid.New(), "ci-runner")
		e.want(rec, http.StatusOK, "")
	})
}

func TestPullsMerge_ImpostazioniEMetodoDisattivato(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	n := e.open("app", "bob", "feat")

	var bs openapi.BranchSettings
	rec := e.do(http.MethodGet, "/repos/alice/app/branch-settings", "carol", "")
	e.want(rec, http.StatusOK, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &bs)
	if !bs.AllowMergeCommit || !bs.AllowSquashMerge || bs.DefaultMergeMethod != "squash" || bs.DeleteBranchOnMerge ||
		bs.RequirePullRequest || bs.RequiredApprovals != 1 || len(bs.RequiredStatusContexts) != 0 {
		t.Fatalf("default inattesi: %+v", bs)
	}

	t.Run("solo_admin_modifica", func(t *testing.T) {
		e.want(e.settings("app", "bob", `{"allowMergeCommit":false}`), http.StatusForbidden, "forbidden")
		e.want(e.settings("app", "carol", `{"allowMergeCommit":false}`), http.StatusForbidden, "forbidden")
	})
	t.Run("almeno_un_metodo_e_default_ammesso", func(t *testing.T) {
		e.want(e.settings("app", "alice", `{"allowMergeCommit":false,"allowSquashMerge":false}`), http.StatusUnprocessableEntity, "no_merge_method")
		e.want(e.settings("app", "alice", `{"allowMergeCommit":false,"defaultMergeMethod":"merge"}`), http.StatusUnprocessableEntity, "default_method_not_allowed")
		e.want(e.settings("app", "alice", `{"requiredApprovals":9}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.settings("app", "alice", `{}`), http.StatusUnprocessableEntity, "validation_failed")
	})
	t.Run("merge_commit_disattivato_e_rifiutato", func(t *testing.T) {
		rec := e.settings("app", "alice", `{"allowMergeCommit":false}`)
		e.want(rec, http.StatusOK, "")
		_ = json.Unmarshal(rec.Body.Bytes(), &bs)
		if bs.AllowMergeCommit || !bs.AllowSquashMerge || bs.DefaultMergeMethod != "squash" {
			t.Fatalf("impostazioni = %+v", bs)
		}
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusConflict, "method_not_allowed")
		m := e.mergeability("app", n)
		if !slices.Equal(m.AllowedMethods, []openapi.MergeMethod{"squash"}) {
			t.Fatalf("allowedMethods = %v", m.AllowedMethods)
		}
		if e.mg.mergeCount() != 0 {
			t.Fatal("git non doveva essere chiamato")
		}
	})
	t.Run("il_default_segue_l_unico_metodo_rimasto", func(t *testing.T) {
		e.want(e.settings("app", "alice", `{"allowMergeCommit":true,"defaultMergeMethod":"merge"}`), http.StatusOK, "")
		rec := e.settings("app", "alice", `{"allowMergeCommit":true,"allowSquashMerge":false}`)
		e.want(rec, http.StatusOK, "")
		_ = json.Unmarshal(rec.Body.Bytes(), &bs)
		if bs.DefaultMergeMethod != "merge" || bs.AllowSquashMerge {
			t.Fatalf("impostazioni = %+v", bs)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "method_not_allowed")
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
	})
	t.Run("repo_archiviato_409", func(t *testing.T) {
		e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoID)
		e.want(e.settings("app", "alice", `{"deleteBranchOnMerge":true}`), http.StatusConflict, "archived")
	})
}

func TestPullsMerge_BozzaRifiutata(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"Bozza","sourceBranch":"feat","draft":true}`)
	e.want(rec, http.StatusCreated, "")
	n := e.pr(rec).Number

	m := e.mergeability("app", n)
	if m.State != "blocked" || m.Mergeable || len(m.Blockers) != 1 || m.Blockers[0].Code != "draft" {
		t.Fatalf("mergeability = %+v", m)
	}
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "draft")
	if e.mg.mergeCount() != 0 {
		t.Fatal("una bozza non si unisce")
	}
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/ready", n), "bob", ""), http.StatusOK, "")
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
}

func TestPullsMerge_ConflittiPR7(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "clash")
	n := e.open("app", "bob", "clash")
	e.mg.conflicts[key(repoID, "clash")] = []string{"src/a.go", "README.md"}

	m := e.mergeability("app", n)
	if m.State != "conflicts" || m.Mergeable || !slices.Equal(m.ConflictFiles, []string{"src/a.go", "README.md"}) ||
		len(m.Blockers) != 1 || m.Blockers[0].Code != "conflicts" || !strings.Contains(m.Blockers[0].Message, "This branch has conflicts") {
		t.Fatalf("mergeability = %+v", m)
	}
	if m.ConflictInstructions == nil || !slices.Contains(*m.ConflictInstructions, "gs pr checkout 1") ||
		!slices.Contains(*m.ConflictInstructions, "git merge origin/main") {
		t.Fatalf("istruzioni = %v", m.ConflictInstructions)
	}

	rec := e.merge("app", n, "bob", `{"method":"squash"}`)
	e.want(rec, http.StatusConflict, "conflicts")
	body := rec.Body.String()
	for _, want := range []string{"src/a.go", "README.md", "gs pr checkout 1", "git merge origin/main"} {
		if !strings.Contains(body, want) {
			t.Fatalf("manca %q nella risposta: %s", want, body)
		}
	}
	if e.mg.mergeCount() != 0 {
		t.Fatal("con i conflitti git non va chiamato")
	}

	// Risolti i conflitti la stessa PR si unisce.
	delete(e.mg.conflicts, key(repoID, "clash"))
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")

	t.Run("troppi_file_in_conflitto_si_troncano", func(t *testing.T) {
		e.branch(repoID, "many")
		n := e.open("app", "bob", "many")
		var files []string
		for i := 0; i < 130; i++ {
			files = append(files, fmt.Sprintf("f%03d.txt", i))
		}
		e.mg.conflicts[key(repoID, "many")] = files
		m := e.mergeability("app", n)
		if len(m.ConflictFiles) != 100 || m.ConflictFilesTruncated == nil || !*m.ConflictFilesTruncated {
			t.Fatalf("file = %d, truncated = %v", len(m.ConflictFiles), m.ConflictFilesTruncated)
		}
	})
}

func TestPullsMerge_PRIndietroSiUnisce(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "old")
	n := e.open("app", "bob", "old")
	e.mg.behind[key(repoID, "old")] = 3

	m := e.mergeability("app", n)
	if m.State != "mergeable" || !m.Mergeable || m.BehindBy != 3 || len(m.Blockers) != 0 {
		t.Fatalf("mergeability = %+v", m)
	}
	e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
}

func TestPullsMerge_HeadCambiatoEStatoNonAperta(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	n := e.open("app", "bob", "feat")

	e.want(e.merge("app", n, "bob", fmt.Sprintf(`{"method":"squash","expectedHeadSha":%q}`, sha(1))), http.StatusConflict, "head_changed")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", n), "bob", ""), http.StatusOK, "")
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "not_open")
	if e.mg.mergeCount() != 0 {
		t.Fatal("git non doveva essere chiamato")
	}
}

func TestPullsMerge_ConcorrentiSullaStessaBase(t *testing.T) {
	t.Run("due_PR_stessa_destinazione_una_sola_riesce", func(t *testing.T) {
		e := newMergeEnv(t)
		repoID := e.repo("app", true)
		e.id.grantWrite(repoID, "bob")
		e.branch(repoID, "one")
		e.branch(repoID, "two")
		n1 := e.open("app", "bob", "one")
		n2 := e.open("app", "bob", "two")
		var wg sync.WaitGroup
		wg.Add(2)
		e.mg.barrier = &wg

		recs := make([]*httptest.ResponseRecorder, 2)
		var done sync.WaitGroup
		for i, n := range []int64{n1, n2} {
			done.Add(1)
			go func() {
				defer done.Done()
				recs[i] = e.merge("app", n, "bob", `{"method":"squash"}`)
			}()
		}
		done.Wait()
		ok, stale := 0, 0
		for _, rec := range recs {
			switch {
			case rec.Code == http.StatusOK:
				ok++
			case rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), `"base_changed"`) && strings.Contains(rec.Body.String(), "riprova"):
				stale++
			default:
				t.Fatalf("risposta inattesa %d %s", rec.Code, rec.Body.String())
			}
		}
		if ok != 1 || stale != 1 || e.mg.mergeCount() != 1 {
			t.Fatalf("riusciti = %d, rifiutati = %d, merge in git = %d", ok, stale, e.mg.mergeCount())
		}
		e.mg.barrier = nil
		// Quello rifiutato è ancora aperto e si può riprovare: ora riesce.
		for i, n := range []int64{n1, n2} {
			if recs[i].Code != http.StatusOK {
				e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
			}
		}
	})

	t.Run("stessa_PR_due_volte_una_sola_riesce", func(t *testing.T) {
		e := newMergeEnv(t)
		repoID := e.repo("app", true)
		e.id.grantWrite(repoID, "bob")
		e.branch(repoID, "feat")
		n := e.open("app", "bob", "feat")
		recs := make([]*httptest.ResponseRecorder, 4)
		var done sync.WaitGroup
		for i := range recs {
			done.Add(1)
			go func() {
				defer done.Done()
				recs[i] = e.merge("app", n, "bob", `{"method":"squash"}`)
			}()
		}
		done.Wait()
		ok := 0
		for _, rec := range recs {
			switch {
			case rec.Code == http.StatusOK:
				ok++
			case rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), `"already_merged"`):
			default:
				t.Fatalf("risposta inattesa %d %s", rec.Code, rec.Body.String())
			}
		}
		if ok != 1 || e.mg.mergeCount() != 1 {
			t.Fatalf("riusciti = %d, merge in git = %d", ok, e.mg.mergeCount())
		}
	})
}

// Regressione GIT-306: con la tx della PR aperta, evaluateMerge leggeva da
// s.pool. Con più merge concorrenti delle connessioni del pool, uno teneva il
// lock di riga aspettando una connessione e gli altri tenevano le connessioni
// ferme sul lock: deadlock. Qui il pool ha 2 connessioni e i merge sono 6.
func TestPullsMerge_PoolPiccoloNonVaInDeadlock(t *testing.T) {
	e := newMergeEnvMaxConns(t, 2)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	n := e.open("app", "bob", "feat")
	recs := make([]*httptest.ResponseRecorder, 6)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var done sync.WaitGroup
		for i := range recs {
			done.Add(1)
			go func() {
				defer done.Done()
				recs[i] = e.merge("app", n, "bob", `{"method":"squash"}`)
			}()
		}
		done.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: i merge concorrenti non sono terminati in 30 s con il pool a 2 connessioni")
	}
	ok := 0
	for _, rec := range recs {
		switch {
		case rec.Code == http.StatusOK:
			ok++
		case rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), `"already_merged"`):
		default:
			t.Fatalf("risposta inattesa %d %s", rec.Code, rec.Body.String())
		}
	}
	if ok != 1 || e.mg.mergeCount() != 1 {
		t.Fatalf("riusciti = %d, merge in git = %d", ok, e.mg.mergeCount())
	}
}

func TestPullsMerge_EliminazioneDelBranch(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	del := func(n int64, user string) *httptest.ResponseRecorder {
		return e.do(http.MethodDelete, fmt.Sprintf("/repos/alice/app/pulls/%d/branch", n), user, "")
	}

	t.Run("manuale_dopo_il_merge", func(t *testing.T) {
		e.branch(repoID, "manual")
		n := e.open("app", "bob", "manual")
		e.want(del(n, "bob"), http.StatusConflict, "not_merged")
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
		if !e.git.has(repoID, "manual") {
			t.Fatal("per default il branch resta dopo il merge (PR3)")
		}
		e.want(del(n, "carol"), http.StatusForbidden, "forbidden")
		e.want(del(n, "bob"), http.StatusNoContent, "")
		if e.git.has(repoID, "manual") {
			t.Fatal("il branch doveva essere eliminato")
		}
		if got := e.prEvents("app", n); !slices.Equal(got, []string{"opened", "merged", "branch_deleted"}) {
			t.Fatalf("cronologia = %v", got)
		}
		x := e.pr(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d", n), "carol", ""))
		if x.SourceBranchExists {
			t.Fatal("sourceBranchExists doveva essere false")
		}
		e.want(del(n, "bob"), http.StatusNotFound, "branch_not_found")
	})

	t.Run("branch_usato_da_un_altra_PR_aperta", func(t *testing.T) {
		e.branch(repoID, "shared")
		e.git.setBranch(repoID, "release", sha(77))
		n1 := e.open("app", "bob", "shared")
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"verso release","sourceBranch":"shared","targetBranch":"release"}`)
		e.want(rec, http.StatusCreated, "")
		e.want(e.merge("app", n1, "bob", `{"method":"squash"}`), http.StatusOK, "")
		e.want(del(n1, "bob"), http.StatusConflict, "branch_in_use")
		if !e.git.has(repoID, "shared") {
			t.Fatal("il branch non andava eliminato")
		}
	})

	t.Run("branch_cambiato_dopo_il_merge_non_si_elimina", func(t *testing.T) {
		e.branch(repoID, "moved")
		n := e.open("app", "bob", "moved")
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
		e.git.setBranch(repoID, "moved", sha(555))
		e.want(del(n, "bob"), http.StatusConflict, "branch_changed")
	})

	t.Run("automatica_su_richiesta_del_merge", func(t *testing.T) {
		e.branch(repoID, "req")
		n := e.open("app", "bob", "req")
		e.want(e.merge("app", n, "bob", `{"method":"squash","deleteBranch":true}`), http.StatusOK, "")
		if e.git.has(repoID, "req") {
			t.Fatal("deleteBranch:true doveva eliminare il branch")
		}
	})

	t.Run("automatica_con_l_impostazione_del_repo", func(t *testing.T) {
		e.want(e.settings("app", "alice", `{"deleteBranchOnMerge":true}`), http.StatusOK, "")
		e.branch(repoID, "auto")
		n := e.open("app", "bob", "auto")
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if e.git.has(repoID, "auto") {
			t.Fatal("deleteBranchOnMerge doveva eliminare il branch")
		}
		if got := e.prEvents("app", n); !slices.Equal(got, []string{"opened", "merged", "branch_deleted"}) {
			t.Fatalf("cronologia = %v", got)
		}
		// Spenta, di nuovo, il branch resta.
		e.want(e.settings("app", "alice", `{"deleteBranchOnMerge":false}`), http.StatusOK, "")
		e.branch(repoID, "stay")
		n = e.open("app", "bob", "stay")
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if !e.git.has(repoID, "stay") {
			t.Fatal("con l'impostazione spenta il branch resta")
		}
	})
}
