//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// M-05/C (GIT-103): issues, numerazione (I1), stato con motivo (I2),
// permessi (I3), modifiche e issue nascoste (I4), repo archiviato (R10).

// issuesIdentity aggiunge al fake di identity il ruolo write (concesso con
// grantWrite) e la risoluzione degli utenti per id.
type issuesIdentity struct {
	*fakeIdentity
	wmu     sync.Mutex
	writers map[uuid.UUID][]string
}

func (f *issuesIdentity) grantWrite(repo uuid.UUID, user string) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.writers[repo] = append(f.writers[repo], users[user])
}

func (f *issuesIdentity) HasRole(ctx context.Context, userID, resourceID uuid.UUID, role string) (bool, error) {
	ok, err := f.fakeIdentity.HasRole(ctx, userID, resourceID, role)
	if err != nil || ok {
		return ok, err
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if (role == "write" || role == "read") && slices.Contains(f.writers[resourceID], userID.String()) {
		return true, nil
	}
	return false, nil
}

func (f *issuesIdentity) LookupUsers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]identityclient.CodeUser, error) {
	out := map[uuid.UUID]identityclient.CodeUser{}
	for name, id := range users {
		u := uuid.MustParse(id)
		if slices.Contains(ids, u) {
			out[u] = identityclient.CodeUser{ID: u, Username: name, Kind: "human"}
		}
	}
	return out, nil
}

var _ identityclient.UserLookup = (*issuesIdentity)(nil)

type issuesEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	id     *issuesIdentity
}

func newIssuesEnv(t *testing.T) *issuesEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	e := &issuesEnv{t: t, pool: pool, id: &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return e
}

func (e *issuesEnv) do(method, path, user, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *issuesEnv) want(rec *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("risposta %d %s, voluto %d", rec.Code, rec.Body.String(), status)
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"`+code+`"`) {
		e.t.Fatalf("risposta %s, voluto il codice %q", rec.Body.String(), code)
	}
}

// repo crea un repo di alice (owner, admin) e ne ritorna l'id. internal =
// leggibile da tutti (P3); altrimenti privato (solo alice).
func (e *issuesEnv) repo(name string, internal bool) uuid.UUID {
	e.t.Helper()
	vis := "private"
	if internal {
		vis = "internal"
	}
	rec := e.do(http.MethodPost, "/repos", "alice", fmt.Sprintf(`{"owner":"alice","name":%q,"visibility":%q}`, name, vis))
	e.want(rec, http.StatusCreated, "")
	return uuid.UUID(*decodeRepo(e.t, rec).Id)
}

func (e *issuesEnv) issue(rec *httptest.ResponseRecorder) openapi.Issue {
	e.t.Helper()
	var x openapi.Issue
	if err := json.Unmarshal(rec.Body.Bytes(), &x); err != nil {
		e.t.Fatalf("issue non valida: %v: %s", err, rec.Body.String())
	}
	return x
}

// open apre una issue come user e ne ritorna il numero.
func (e *issuesEnv) open(repo, user, title string) int64 {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/issues", user, fmt.Sprintf(`{"title":%q,"body":"testo"}`, title))
	e.want(rec, http.StatusCreated, "")
	return e.issue(rec).Number
}

func (e *issuesEnv) events(repo string, n int64, user string) []openapi.IssueEvent {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/%s/issues/%d/events", repo, n), user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.IssueEventList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func eventTypes(evs []openapi.IssueEvent) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, string(ev.Type))
	}
	return out
}

func (e *issuesEnv) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func TestIssues_CreazioneENumerazioneI1(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")

	t.Run("numeri_progressivi_dal_contatore_e_evento_opened", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"  Primo bug ","body":"**ciao**"}`)
		e.want(rec, http.StatusCreated, "")
		x := e.issue(rec)
		if x.Number != 1 || x.Title != "Primo bug" || x.Body != "**ciao**" || x.State != "open" || x.CloseReason != nil ||
			x.Author.Username != "carol" || x.Edited || x.Hidden || x.Locked || x.CommentCount != 0 || len(x.Labels) != 0 || len(x.Assignees) != 0 {
			t.Fatalf("issue inattesa: %+v", x)
		}
		if loc := rec.Header().Get("Location"); loc != "/repos/alice/app/issues/1" {
			t.Fatalf("Location = %q", loc)
		}
		if n := e.open("app", "carol", "Secondo"); n != 2 {
			t.Fatalf("numero = %d, voluto 2", n)
		}
		if got := eventTypes(e.events("app", 1, "carol")); !slices.Equal(got, []string{"opened"}) {
			t.Fatalf("cronologia = %v", got)
		}
		ev := e.events("app", 1, "carol")[0]
		if ev.Actor == nil || ev.Actor.Username != "carol" {
			t.Fatalf("attore = %+v", ev.Actor)
		}
		var next int64
		if err := e.pool.QueryRow(context.Background(), `SELECT next_number FROM core.repo_counters WHERE repo_id = $1`, repoID).Scan(&next); err != nil || next != 3 {
			t.Fatalf("next_number = %d (%v), voluto 3", next, err)
		}
	})

	t.Run("il_numero_non_si_riusa_dopo_chiusura_o_nascondimento", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues/2/close", "carol", `{}`), http.StatusOK, "")
		e.want(e.do(http.MethodPut, "/repos/alice/app/issues/2/hidden", "alice", `{"hidden":true}`), http.StatusOK, "")
		if n := e.open("app", "carol", "Terzo"); n != 3 {
			t.Fatalf("numero = %d, voluto 3 (mai riusato)", n)
		}
	})

	t.Run("il_contatore_e_unico_per_repo", func(t *testing.T) {
		e.repo("other", true)
		if n := e.open("other", "carol", "Altro repo"); n != 1 {
			t.Fatalf("numero in un altro repo = %d, voluto 1", n)
		}
	})

	t.Run("il_contatore_e_condiviso_con_le_pull_request", func(t *testing.T) {
		// I1: una PR prende il suo numero dallo stesso contatore, quindi la
		// issue successiva salta quel numero; e un INSERT che non passa dal
		// contatore con il numero di una PR è rifiutato dallo schema.
		e.repo("shared", true)
		if n := e.open("shared", "carol", "prima"); n != 1 {
			t.Fatalf("numero = %d", n)
		}
		e.sql(`WITH c AS (UPDATE core.repo_counters SET next_number = next_number + 1
				WHERE repo_id = (SELECT resource_id FROM core.repositories WHERE name = 'shared') RETURNING repo_id, next_number - 1 AS n)
			INSERT INTO core.pull_requests (id, repo_id, number, source_branch, target_branch, title, author_id)
			SELECT gen_random_uuid(), repo_id, n, 'feat', 'main', 'una PR', $1 FROM c`, uuid.MustParse(aliceID))
		if n := e.open("shared", "carol", "dopo la PR"); n != 3 {
			t.Fatalf("numero = %d, voluto 3 (il 2 è della PR)", n)
		}
		_, err := e.pool.Exec(context.Background(), `INSERT INTO core.issues (id, repo_id, number, title, author_id)
			SELECT gen_random_uuid(), repo_id, 2, 'collide', $1 FROM core.pull_requests WHERE number = 2`, uuid.MustParse(aliceID))
		if err == nil {
			t.Fatal("un'issue con il numero di una PR doveva essere rifiutata")
		}
	})

	t.Run("numeri_unici_e_contigui_sotto_concorrenza", func(t *testing.T) {
		e.repo("race", true)
		const n = 25
		var wg sync.WaitGroup
		numbers := make(chan int64, n)
		errs := make(chan string, n)
		users := []string{"alice", "bob", "carol"}
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := e.do(http.MethodPost, "/repos/alice/race/issues", users[i%len(users)], fmt.Sprintf(`{"title":"t%d"}`, i))
				if rec.Code != http.StatusCreated {
					errs <- fmt.Sprintf("%d %s", rec.Code, rec.Body.String())
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
		for msg := range errs {
			t.Fatalf("creazione concorrente fallita: %s", msg)
		}
		var got []int64
		for v := range numbers {
			got = append(got, v)
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		for i, v := range got {
			if v != int64(i+1) {
				t.Fatalf("numeri %v: attesi 1..%d tutti distinti e contigui", got, n)
			}
		}
		if len(got) != n {
			t.Fatalf("create %d issue, volute %d", len(got), n)
		}
	})
}

func TestIssues_CreazioneRegoleI3EValidazione(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.repo("secret", false)
	e.sql(`INSERT INTO core.milestones (id, repo_id, number, title) VALUES ($1, $2, 1, 'v1')`, uuid.New(), repoID)
	e.sql(`UPDATE core.repo_counters SET next_milestone_number = 2 WHERE repo_id = $1`, repoID)

	t.Run("chi_ha_solo_read_apre", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"apro io"}`), http.StatusCreated, "")
	})
	t.Run("repo_non_leggibile_404_anche_per_la_creazione", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/secret/issues", "carol", `{"title":"x"}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPost, "/repos/alice/inesistente/issues", "alice", `{"title":"x"}`), http.StatusNotFound, "not_found")
		n := e.open("secret", "alice", "dell'owner")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/secret/issues/%d", n), "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, "/repos/alice/secret/issues", "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPatch, fmt.Sprintf("/repos/alice/secret/issues/%d", n), "carol", `{"title":"y"}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/secret/issues/%d/close", n), "carol", `{}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/secret/issues/%d/reopen", n), "carol", ``), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/secret/issues/%d/hidden", n), "carol", `{"hidden":true}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/secret/issues/%d/events", n), "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/secret/issues/%d/versions", n), "carol", ""), http.StatusNotFound, "not_found")
	})
	t.Run("etichette_e_milestone_richiedono_write", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","labels":["bug"]}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","milestone":1}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","assignees":["bob"]}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"con etichette","labels":["bug","Good First Issue"],"milestone":1}`)
		e.want(rec, http.StatusCreated, "")
		x := e.issue(rec)
		if len(x.Labels) != 2 || x.Labels[0].Name != "bug" || x.Labels[1].Name != "good first issue" || x.Milestone == nil || x.Milestone.Number != 1 || x.Milestone.Title != "v1" {
			t.Fatalf("etichette o milestone inattese: %+v", x)
		}
		if got := eventTypes(e.events("app", x.Number, "bob")); !slices.Equal(got, []string{"opened", "labeled", "labeled", "milestoned"}) {
			t.Fatalf("cronologia = %v", got)
		}
	})
	t.Run("etichetta_o_milestone_inesistente_422_e_nessuna_issue_creata", func(t *testing.T) {
		var before int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, repoID).Scan(&before)
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"x","labels":["nope"]}`), http.StatusUnprocessableEntity, "label_not_found")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"x","milestone":99}`), http.StatusUnprocessableEntity, "milestone_not_found")
		var after int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, repoID).Scan(&after)
		if after != before {
			t.Fatalf("issues %d -> %d: la creazione fallita non deve lasciare righe", before, after)
		}
		// Il numero della creazione fallita non è stato consumato.
		x := e.issue(e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"dopo"}`))
		if x.Number != int64(before)+1 {
			t.Fatalf("numero = %d, voluto %d", x.Number, before+1)
		}
	})
	t.Run("validazione", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"   "}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"`+strings.Repeat("a", 257)+`"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","body":"`+strings.Repeat("a", 65537)+`"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","body":"`+strings.Repeat("a", 1<<20)+`"}`), http.StatusRequestEntityTooLarge, "body_too_large")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"body":"senza titolo"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"x","extra":1}`), http.StatusBadRequest, "bad_request")
	})
}

func TestIssues_LetturaEElenco(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	for i := 1; i <= 5; i++ {
		e.open("app", "carol", fmt.Sprintf("issue %d", i))
	}
	e.want(e.do(http.MethodPost, "/repos/alice/app/issues/2/close", "bob", `{"reason":"not_planned"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, "/repos/alice/app/issues/3/close", "bob", `{}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, "/repos/alice/app/issues/4/close", "bob", `{"reason":"duplicate","duplicateOf":1}`), http.StatusOK, "")
	e.sql(`INSERT INTO core.issue_labels (issue_id, label_id)
		SELECT i.id, l.id FROM core.issues i, core.labels l WHERE i.repo_id = $1 AND i.number IN (1, 2) AND l.repo_id = $1 AND l.name = 'bug'`, repoID)
	e.sql(`INSERT INTO core.issue_labels (issue_id, label_id)
		SELECT i.id, l.id FROM core.issues i, core.labels l WHERE i.repo_id = $1 AND i.number = 1 AND l.repo_id = $1 AND l.name = 'question'`, repoID)

	list := func(query string) openapi.IssueList {
		t.Helper()
		rec := e.do(http.MethodGet, "/repos/alice/app/issues"+query, "carol", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.IssueList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	numbers := func(l openapi.IssueList) []int64 {
		out := []int64{}
		for _, it := range l.Items {
			out = append(out, it.Number)
		}
		return out
	}

	t.Run("lettura_per_numero", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/repos/alice/app/issues/4", "carol", "")
		e.want(rec, http.StatusOK, "")
		x := e.issue(rec)
		if x.Number != 4 || x.State != "closed" || x.CloseReason == nil || *x.CloseReason != "duplicate" || x.DuplicateOf == nil || *x.DuplicateOf != 1 || x.ClosedAt == nil {
			t.Fatalf("issue = %+v", x)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues/99", "carol", ""), http.StatusNotFound, "not_found")
	})
	t.Run("default_aperte_dalla_piu_recente", func(t *testing.T) {
		l := list("")
		if got := numbers(l); !slices.Equal(got, []int64{5, 1}) || l.Total != 2 || l.Page != 1 || l.PerPage != 20 {
			t.Fatalf("elenco = %v %+v", got, l)
		}
		if l.Items[1].Number != 1 || len(l.Items[1].Labels) != 2 || l.Items[1].Author.Username != "carol" {
			t.Fatalf("riassunto = %+v", l.Items[1])
		}
	})
	t.Run("filtri_stato_motivo_etichetta", func(t *testing.T) {
		if got := numbers(list("?state=closed")); !slices.Equal(got, []int64{4, 3, 2}) {
			t.Fatalf("closed = %v", got)
		}
		if got := numbers(list("?state=all")); !slices.Equal(got, []int64{5, 4, 3, 2, 1}) {
			t.Fatalf("all = %v", got)
		}
		if got := numbers(list("?state=all&reason=not_planned")); !slices.Equal(got, []int64{2}) {
			t.Fatalf("reason = %v", got)
		}
		if got := numbers(list("?state=all&labels=bug")); !slices.Equal(got, []int64{2, 1}) {
			t.Fatalf("labels = %v", got)
		}
		if got := numbers(list("?state=all&labels=Bug,question")); !slices.Equal(got, []int64{1}) {
			t.Fatalf("labels (tutte) = %v", got)
		}
		if got := numbers(list("?state=all&labels=nope")); len(got) != 0 {
			t.Fatalf("etichetta ignota = %v", got)
		}
	})
	t.Run("paginazione_e_ordinamento", func(t *testing.T) {
		l := list("?state=all&perPage=2&page=2")
		if got := numbers(l); !slices.Equal(got, []int64{3, 2}) || l.Total != 5 {
			t.Fatalf("pagina 2 = %v %+v", got, l)
		}
		if got := numbers(list("?state=all&sort=updated")); len(got) != 5 {
			t.Fatalf("sort=updated = %v", got)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues?page=0", "carol", ""), http.StatusBadRequest, "invalid_page")
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues?perPage=101", "carol", ""), http.StatusBadRequest, "invalid_per_page")
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues?sort=relevance", "carol", ""), http.StatusUnprocessableEntity, "validation_failed")
	})
	t.Run("ricerca_completa_non_ancora_disponibile", func(t *testing.T) {
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues?q=is:open", "carol", ""), http.StatusNotImplemented, "not_implemented")
	})
}

func TestIssues_ModificaI4(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "Titolo")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)

	t.Run("solo_l_autore_modifica_anche_con_write_o_admin", func(t *testing.T) {
		e.want(e.do(http.MethodPatch, path, "bob", `{"title":"hack"}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPatch, path, "alice", `{"body":"hack"}`), http.StatusForbidden, "forbidden")
		if x := e.issue(e.do(http.MethodGet, path, "carol", "")); x.Title != "Titolo" || x.Edited {
			t.Fatalf("la issue non doveva cambiare: %+v", x)
		}
	})
	t.Run("validazione", func(t *testing.T) {
		e.want(e.do(http.MethodPatch, path, "carol", `{}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPatch, path, "carol", `{"title":""}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPatch, path, "carol", `{"stato":"closed"}`), http.StatusBadRequest, "bad_request")
		e.want(e.do(http.MethodPatch, "/repos/alice/app/issues/99", "carol", `{"title":"x"}`), http.StatusNotFound, "not_found")
	})
	t.Run("modifica_conserva_le_versioni_e_segna_edited", func(t *testing.T) {
		rec := e.do(http.MethodPatch, path, "carol", `{"title":"Titolo nuovo","body":"corpo 2"}`)
		e.want(rec, http.StatusOK, "")
		if x := e.issue(rec); x.Title != "Titolo nuovo" || x.Body != "corpo 2" || !x.Edited {
			t.Fatalf("issue = %+v", x)
		}
		e.want(e.do(http.MethodPatch, path, "carol", `{"body":"corpo 3"}`), http.StatusOK, "")
		// Nessuna modifica effettiva: nessuna versione né evento.
		e.want(e.do(http.MethodPatch, path, "carol", `{"body":"corpo 3"}`), http.StatusOK, "")

		rec = e.do(http.MethodGet, path+"/versions", "alice", "")
		e.want(rec, http.StatusOK, "")
		var vl openapi.TextVersionList
		if err := json.Unmarshal(rec.Body.Bytes(), &vl); err != nil {
			t.Fatal(err)
		}
		if len(vl.Items) != 2 {
			t.Fatalf("versioni = %+v", vl.Items)
		}
		v2, v1 := vl.Items[0], vl.Items[1]
		if v1.Version != 1 || v1.Body != "testo" || v1.Title == nil || *v1.Title != "Titolo" || v1.Editor.Username != "carol" {
			t.Fatalf("versione 1 (originale) = %+v", v1)
		}
		if v2.Version != 2 || v2.Body != "corpo 2" || v2.Title == nil || *v2.Title != "Titolo nuovo" {
			t.Fatalf("versione 2 = %+v", v2)
		}
		if got := eventTypes(e.events("app", n, "carol")); !slices.Equal(got, []string{"opened", "renamed", "edited", "edited"}) {
			t.Fatalf("cronologia = %v", got)
		}
		ev := e.events("app", n, "carol")[1]
		if (*ev.Data)["from"] != "Titolo" || (*ev.Data)["to"] != "Titolo nuovo" {
			t.Fatalf("evento renamed = %+v", ev.Data)
		}
	})
	t.Run("le_versioni_sono_per_admin", func(t *testing.T) {
		e.want(e.do(http.MethodGet, path+"/versions", "carol", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodGet, path+"/versions", "bob", ""), http.StatusForbidden, "forbidden")
		m := e.open("app", "carol", "mai modificata")
		rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/issues/%d/versions", m), "alice", "")
		e.want(rec, http.StatusOK, "")
		if !strings.Contains(rec.Body.String(), `"items":[]`) {
			t.Fatalf("attesa lista vuota, risposta %s", rec.Body.String())
		}
	})
	t.Run("discussione_bloccata_l_autore_con_solo_read_ha_403_locked", func(t *testing.T) {
		e.sql(`UPDATE core.issues SET locked = true WHERE repo_id = $1 AND number = $2`, repoID, n)
		e.want(e.do(http.MethodPatch, path, "carol", `{"body":"da bloccata"}`), http.StatusForbidden, "locked")
		e.sql(`UPDATE core.issues SET locked = false WHERE repo_id = $1 AND number = $2`, repoID, n)
	})
	t.Run("l_autore_con_write_modifica_anche_se_bloccata", func(t *testing.T) {
		b := e.open("app", "bob", "di bob")
		e.sql(`UPDATE core.issues SET locked = true WHERE repo_id = $1 AND number = $2`, repoID, b)
		e.want(e.do(http.MethodPatch, fmt.Sprintf("/repos/alice/app/issues/%d", b), "bob", `{"body":"ok"}`), http.StatusOK, "")
	})
}

func TestIssues_StatoConMotivoI2ePermessiI3(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	other := e.open("app", "alice", "da duplicare")
	cp := func(n int64, op string) string { return fmt.Sprintf("/repos/alice/app/issues/%d/%s", n, op) }

	t.Run("chiusura_con_ciascun_motivo", func(t *testing.T) {
		a := e.open("app", "carol", "a")
		rec := e.do(http.MethodPost, cp(a, "close"), "carol", ``)
		e.want(rec, http.StatusOK, "")
		x := e.issue(rec)
		if x.State != "closed" || x.CloseReason == nil || *x.CloseReason != "completed" || x.ClosedAt == nil || x.DuplicateOf != nil {
			t.Fatalf("chiusura di default = %+v", x)
		}
		b := e.open("app", "carol", "b")
		x = e.issue(e.do(http.MethodPost, cp(b, "close"), "carol", `{"reason":"not_planned"}`))
		if x.State != "closed" || *x.CloseReason != "not_planned" {
			t.Fatalf("not_planned = %+v", x)
		}
		c := e.open("app", "carol", "c")
		x = e.issue(e.do(http.MethodPost, cp(c, "close"), "carol", fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, other)))
		if x.State != "closed" || *x.CloseReason != "duplicate" || x.DuplicateOf == nil || *x.DuplicateOf != other {
			t.Fatalf("duplicate = %+v", x)
		}
		evs := e.events("app", c, "carol")
		last := evs[len(evs)-1]
		if last.Type != "closed" || (*last.Data)["reason"] != "duplicate" || (*last.Data)["duplicateOf"] != float64(other) {
			t.Fatalf("evento closed = %+v", last)
		}
	})
	t.Run("motivo_non_valido_o_incoerente_422_senza_chiudere", func(t *testing.T) {
		n := e.open("app", "carol", "d")
		for _, body := range []string{
			`{"reason":"duplicate"}`,                                  // manca duplicateOf
			`{"reason":"completed","duplicateOf":1}`,                  // duplicateOf senza duplicate
			fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, n), // se stessa
			`{"reason":"duplicate","duplicateOf":9999}`,               // inesistente
			`{"reason":"duplicate","duplicateOf":0}`,
			`{"reason":"boh"}`,
		} {
			e.want(e.do(http.MethodPost, cp(n, "close"), "carol", body), http.StatusUnprocessableEntity, "validation_failed")
		}
		if x := e.issue(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/issues/%d", n), "carol", "")); x.State != "open" {
			t.Fatalf("la issue doveva restare aperta: %+v", x)
		}
	})
	t.Run("doppia_chiusura_e_doppia_riapertura_409", func(t *testing.T) {
		n := e.open("app", "carol", "e")
		e.want(e.do(http.MethodPost, cp(n, "reopen"), "carol", ``), http.StatusConflict, "already_open")
		e.want(e.do(http.MethodPost, cp(n, "close"), "carol", `{}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, cp(n, "close"), "carol", `{"reason":"not_planned"}`), http.StatusConflict, "already_closed")
	})
	t.Run("riaprire_azzera_motivo_e_duplicato", func(t *testing.T) {
		n := e.open("app", "carol", "f")
		e.want(e.do(http.MethodPost, cp(n, "close"), "carol", fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, other)), http.StatusOK, "")
		rec := e.do(http.MethodPost, cp(n, "reopen"), "carol", ``)
		e.want(rec, http.StatusOK, "")
		x := e.issue(rec)
		if x.State != "open" || x.CloseReason != nil || x.DuplicateOf != nil || x.ClosedAt != nil {
			t.Fatalf("riaperta = %+v", x)
		}
		if strings.Contains(rec.Body.String(), "closeReason") || strings.Contains(rec.Body.String(), "duplicateOf") {
			t.Fatalf("il motivo non deve comparire: %s", rec.Body.String())
		}
		var reason *string
		var dup *int64
		if err := e.pool.QueryRow(context.Background(), `SELECT close_reason, duplicate_of FROM core.issues WHERE repo_id=$1 AND number=$2`, repoID, n).Scan(&reason, &dup); err != nil || reason != nil || dup != nil {
			t.Fatalf("colonne dopo la riapertura: %v %v %v", reason, dup, err)
		}
		// Si richiude con un altro motivo.
		x = e.issue(e.do(http.MethodPost, cp(n, "close"), "carol", `{"reason":"not_planned"}`))
		if *x.CloseReason != "not_planned" || x.DuplicateOf != nil {
			t.Fatalf("seconda chiusura = %+v", x)
		}
		if got := eventTypes(e.events("app", n, "carol")); !slices.Equal(got, []string{"opened", "closed", "reopened", "closed"}) {
			t.Fatalf("cronologia = %v", got)
		}
	})
	t.Run("I3_chi_ha_solo_read_non_chiude_ne_riapre_quelle_altrui", func(t *testing.T) {
		n := e.open("app", "alice", "di alice")
		e.want(e.do(http.MethodPost, cp(n, "close"), "carol", `{}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPost, cp(n, "close"), "alice", `{}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, cp(n, "reopen"), "carol", ``), http.StatusForbidden, "forbidden")
		if x := e.issue(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/issues/%d", n), "carol", "")); x.State != "closed" {
			t.Fatalf("stato = %s", x.State)
		}
	})
	t.Run("I3_chi_ha_write_chiude_e_riapre_quelle_altrui", func(t *testing.T) {
		n := e.open("app", "carol", "di carol")
		e.want(e.do(http.MethodPost, cp(n, "close"), "bob", `{}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, cp(n, "reopen"), "bob", ``), http.StatusOK, "")
	})
	t.Run("I3_l_autore_con_solo_read_chiude_e_riapre_la_propria", func(t *testing.T) {
		n := e.open("app", "carol", "mia")
		e.want(e.do(http.MethodPost, cp(n, "close"), "carol", `{"reason":"not_planned"}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, cp(n, "reopen"), "carol", ``), http.StatusOK, "")
	})
	t.Run("issue_inesistente_404", func(t *testing.T) {
		e.want(e.do(http.MethodPost, cp(999, "close"), "alice", `{}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPost, cp(999, "reopen"), "alice", ``), http.StatusNotFound, "not_found")
	})
	t.Run("le_chiusure_concorrenti_ne_vincono_una_sola", func(t *testing.T) {
		n := e.open("app", "carol", "gara")
		var wg sync.WaitGroup
		codes := make(chan int, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes <- e.do(http.MethodPost, cp(n, "close"), "bob", `{}`).Code
			}()
		}
		wg.Wait()
		close(codes)
		ok, conflict := 0, 0
		for c := range codes {
			switch c {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				conflict++
			}
		}
		if ok != 1 || conflict != 5 {
			t.Fatalf("200=%d 409=%d, voluti 1 e 5", ok, conflict)
		}
		closed := 0
		for _, ev := range e.events("app", n, "bob") {
			if ev.Type == "closed" {
				closed++
			}
		}
		if closed != 1 {
			t.Fatalf("eventi closed = %d, voluto 1", closed)
		}
	})
}

func TestIssues_NascosteI4(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "da nascondere")
	other := e.open("app", "carol", "visibile")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)

	t.Run("solo_admin_nasconde", func(t *testing.T) {
		e.want(e.do(http.MethodPut, path+"/hidden", "carol", `{"hidden":true}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, path+"/hidden", "bob", `{"hidden":true}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, path+"/hidden", "alice", `{}`), http.StatusBadRequest, "bad_request")
		rec := e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":true}`)
		e.want(rec, http.StatusOK, "")
		if x := e.issue(rec); !x.Hidden || x.Number != n || x.Title != "da nascondere" {
			t.Fatalf("issue = %+v", x)
		}
		// Idempotente: nessun secondo evento.
		e.want(e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":true}`), http.StatusOK, "")
	})
	t.Run("per_gli_altri_e_come_inesistente_in_ogni_operazione", func(t *testing.T) {
		for _, user := range []string{"carol", "bob"} { // anche l'autore e chi ha write
			e.want(e.do(http.MethodGet, path, user, ""), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodPatch, path, user, `{"title":"x"}`), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodPost, path+"/close", user, `{}`), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodPost, path+"/reopen", user, ``), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodPut, path+"/hidden", user, `{"hidden":false}`), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodGet, path+"/events", user, ""), http.StatusNotFound, "not_found")
			e.want(e.do(http.MethodGet, path+"/versions", user, ""), http.StatusNotFound, "not_found")
		}
	})
	t.Run("non_compare_negli_elenchi_dei_non_admin_ma_si_a_admin", func(t *testing.T) {
		count := func(user string) (int, []int64) {
			rec := e.do(http.MethodGet, "/repos/alice/app/issues?state=all", user, "")
			e.want(rec, http.StatusOK, "")
			var l openapi.IssueList
			_ = json.Unmarshal(rec.Body.Bytes(), &l)
			var nums []int64
			for _, it := range l.Items {
				nums = append(nums, it.Number)
			}
			return l.Total, nums
		}
		if total, nums := count("carol"); total != 1 || !slices.Equal(nums, []int64{other}) {
			t.Fatalf("carol: %d %v", total, nums)
		}
		if total, _ := count("alice"); total != 2 {
			t.Fatalf("alice (admin) deve vedere anche la nascosta: %d", total)
		}
	})
	t.Run("admin_legge_modifica_stato_e_cronologia", func(t *testing.T) {
		x := e.issue(e.do(http.MethodGet, path, "alice", ""))
		if !x.Hidden || x.Body != "testo" {
			t.Fatalf("issue = %+v", x)
		}
		e.want(e.do(http.MethodPost, path+"/close", "alice", `{}`), http.StatusOK, "")
		if got := eventTypes(e.events("app", n, "alice")); !slices.Equal(got, []string{"opened", "hidden", "closed"}) {
			t.Fatalf("cronologia = %v", got)
		}
	})
	t.Run("il_numero_si_conserva_e_non_si_riusa", func(t *testing.T) {
		if m := e.open("app", "carol", "nuova"); m != other+1 {
			t.Fatalf("numero = %d, voluto %d", m, other+1)
		}
	})
	t.Run("un_duplicato_nascosto_non_esiste_per_chi_non_e_admin", func(t *testing.T) {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", other), "bob", fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, n)), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", other), "alice", fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, n)), http.StatusOK, "")
	})
	t.Run("rimostrare", func(t *testing.T) {
		rec := e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":false}`)
		e.want(rec, http.StatusOK, "")
		e.want(e.do(http.MethodGet, path, "carol", ""), http.StatusOK, "")
		if got := eventTypes(e.events("app", n, "alice")); !slices.Equal(got, []string{"opened", "hidden", "closed", "unhidden"}) {
			t.Fatalf("cronologia = %v", got)
		}
	})
}

func TestIssues_RepoArchiviatoR10(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	openIssue := e.open("app", "carol", "aperta")
	closedIssue := e.open("app", "carol", "chiusa")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", closedIssue), "carol", `{}`), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/app", "alice", `{"archived":true}`), http.StatusOK, "")

	p := func(n int64, op string) string { return fmt.Sprintf("/repos/alice/app/issues/%d%s", n, op) }
	t.Run("creazione_e_modifica_rifiutate_409", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"nuova"}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "alice", `{"title":"nuova"}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPatch, p(openIssue, ""), "carol", `{"title":"cambiato"}`), http.StatusConflict, "archived")
	})
	t.Run("chiusura_riapertura_e_nascondimento_rifiutati_409", func(t *testing.T) {
		e.want(e.do(http.MethodPost, p(openIssue, "/close"), "bob", `{}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPost, p(closedIssue, "/reopen"), "bob", ``), http.StatusConflict, "archived")
		e.want(e.do(http.MethodPut, p(openIssue, "/hidden"), "alice", `{"hidden":true}`), http.StatusConflict, "archived")
	})
	t.Run("lettura_possibile", func(t *testing.T) {
		x := e.issue(e.do(http.MethodGet, p(openIssue, ""), "carol", ""))
		if x.Title != "aperta" || x.State != "open" {
			t.Fatalf("issue = %+v", x)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/app/issues?state=all", "carol", ""), http.StatusOK, "")
		e.want(e.do(http.MethodGet, p(openIssue, "/events"), "carol", ""), http.StatusOK, "")
		e.want(e.do(http.MethodGet, p(openIssue, "/versions"), "alice", ""), http.StatusOK, "")
	})
	t.Run("nulla_e_cambiato_e_il_contatore_non_e_avanzato", func(t *testing.T) {
		var n int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, repoID).Scan(&n)
		var next int64
		_ = e.pool.QueryRow(context.Background(), `SELECT next_number FROM core.repo_counters WHERE repo_id = $1`, repoID).Scan(&next)
		if n != 2 || next != 3 {
			t.Fatalf("issues = %d, next_number = %d, voluti 2 e 3", n, next)
		}
	})
	t.Run("riattivato_il_repo_le_issues_si_modificano_di_nuovo", func(t *testing.T) {
		e.want(e.do(http.MethodPatch, "/repos/alice/app", "alice", `{"archived":false}`), http.StatusOK, "")
		if n := e.open("app", "carol", "dopo"); n != 3 {
			t.Fatalf("numero = %d, voluto 3", n)
		}
	})
}

func TestIssues_CronologiaEPaginazione(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "storia")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	e.want(e.do(http.MethodPatch, path, "carol", `{"title":"storia 2"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, path+"/close", "bob", `{"reason":"not_planned"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, path+"/reopen", "carol", ``), http.StatusOK, "")

	evs := e.events("app", n, "carol")
	if got := eventTypes(evs); !slices.Equal(got, []string{"opened", "renamed", "closed", "reopened"}) {
		t.Fatalf("cronologia = %v", got)
	}
	for i, want := range []string{"carol", "carol", "bob", "carol"} {
		if evs[i].Actor == nil || evs[i].Actor.Username != want {
			t.Fatalf("evento %d: attore %+v, voluto %s", i, evs[i].Actor, want)
		}
	}
	if (*evs[2].Data)["reason"] != "not_planned" {
		t.Fatalf("evento closed = %+v", evs[2].Data)
	}
	rec := e.do(http.MethodGet, path+"/events?perPage=2&page=2", "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.IssueEventList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	if l.Total != 4 || len(l.Items) != 2 || l.Items[0].Type != "closed" {
		t.Fatalf("pagina 2 = %+v", l)
	}
	e.want(e.do(http.MethodGet, path+"/events?page=0", "carol", ""), http.StatusBadRequest, "invalid_page")
	e.want(e.do(http.MethodGet, "/repos/alice/app/issues/99/events", "carol", ""), http.StatusNotFound, "not_found")
}
