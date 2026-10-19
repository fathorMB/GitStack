//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-266 (PR12b): la casella Viewed di Files changed è personale e si
// azzera se il file cambia con un nuovo commit. Il servizio git finto
// risponde al confronto «dal commit visto alla punta» con i file di setSince.

const changedA = `[{"path":"a.txt","status":"modified","additions":1,"deletions":1,"binary":false,"truncated":false,"patch":"@@ -3,1 +3,1 @@\n-c\n+z\n"}]`

func (e *reviewEnv) setViewed(user string, n int64, path string, viewed bool) int {
	e.t.Helper()
	body := fmt.Sprintf(`{"path":%q,"viewed":%t}`, path, viewed)
	return e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/pulls/%d/viewed", n), user, body).Code
}

func (e *reviewEnv) viewedOf(user string, n int64) []string {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/files", n), user, "")
	e.want(rec, http.StatusOK, "")
	var f openapi.PullRequestFiles
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		e.t.Fatal(err)
	}
	return f.ViewedPaths
}

func (e *reviewEnv) mustViewed(user string, n int64, path string, viewed bool) {
	e.t.Helper()
	if c := e.setViewed(user, n, path, viewed); c != http.StatusOK {
		e.t.Fatalf("%s: viewed %s=%t: stato %d", user, path, viewed, c)
	}
}

func TestPullViewed_PR12b_Personale(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")

	e.mustViewed("carol", n, "a.txt", true) // anche con il solo read
	e.mustViewed("bob", n, "b.txt", true)
	if got := e.viewedOf("carol", n); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("carol = %v", got)
	}
	if got := e.viewedOf("bob", n); !slices.Equal(got, []string{"b.txt"}) {
		t.Fatalf("bob = %v", got)
	}
	if got := e.viewedOf("alice", n); len(got) != 0 {
		t.Fatalf("alice = %v", got)
	}
	// idempotente; smarcare è solo per sé e anche un file mai marcato va bene
	e.mustViewed("carol", n, "a.txt", true)
	e.mustViewed("carol", n, "b.txt", false)
	if got := e.viewedOf("bob", n); !slices.Equal(got, []string{"b.txt"}) {
		t.Fatalf("bob dopo lo smarca di carol = %v", got)
	}
	e.mustViewed("carol", n, "a.txt", false)
	if got := e.viewedOf("carol", n); len(got) != 0 {
		t.Fatalf("carol dopo lo smarca = %v", got)
	}
	// anche il filtro ?path= mostra lo stato di chi guarda
	e.mustViewed("carol", n, "b.txt", true)
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/files?path=b.txt", n), "carol", "")
	e.want(rec, http.StatusOK, "")
	var f openapi.PullRequestFiles
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || len(f.Files) != 1 || !slices.Equal(f.ViewedPaths, []string{"b.txt"}) {
		t.Fatalf("?path= = %v %+v", err, f)
	}
}

func TestPullViewed_PR12b_InputEPermessi(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")

	if c := e.setViewed("carol", n, "non/esiste.txt", true); c != http.StatusUnprocessableEntity {
		t.Fatalf("file non della PR: stato %d", c)
	}
	if c := e.setViewed("carol", n, "", true); c != http.StatusUnprocessableEntity {
		t.Fatalf("path vuoto: stato %d", c)
	}
	e.want(e.do(http.MethodPut, "/repos/alice/app/pulls/999/viewed", "carol", `{"path":"a.txt","viewed":true}`), http.StatusNotFound, "not_found")
	// il numero di una issue non è una PR
	is := e.pullsEnv.issuesEnv.open("app", "alice", "issue")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/pulls/%d/viewed", is), "carol", `{"path":"a.txt","viewed":true}`), http.StatusNotFound, "not_found")
	// repo privato che carol non legge: 404 come ogni altra operazione
	secret := e.pullsEnv.repo("secret", false)
	e.branch(secret, "x")
	sn := e.pullsEnv.open("secret", "alice", "x")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/secret/pulls/%d/viewed", sn), "carol", `{"path":"a.txt","viewed":true}`), http.StatusNotFound, "not_found")
	// repo archiviato: 409 archived
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repo)
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/pulls/%d/viewed", n), "carol", `{"path":"a.txt","viewed":true}`), http.StatusConflict, "archived")
}

func TestPullViewed_PR12b_SiAzzeraSeIlFileCambia(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")
	for _, u := range []string{"carol", "bob"} {
		e.mustViewed(u, n, "a.txt", true)
		e.mustViewed(u, n, "b.txt", true)
	}

	// nuovo commit che cambia solo a.txt: a.txt torna non visto, b.txt resta
	e.git.setSince(changedA)
	e.git.setBranch(e.repo, "feat", sha(5000))
	for _, u := range []string{"carol", "bob"} {
		if got := e.viewedOf(u, n); !slices.Equal(got, []string{"b.txt"}) {
			t.Fatalf("%s dopo il nuovo commit = %v, voluto solo b.txt", u, got)
		}
	}
	// il file torna visto solo se lo si marca di nuovo, sul nuovo head
	e.mustViewed("carol", n, "a.txt", true)
	if got := e.viewedOf("carol", n); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("carol dopo la nuova spunta = %v", got)
	}
	if got := e.viewedOf("bob", n); !slices.Equal(got, []string{"b.txt"}) {
		t.Fatalf("bob = %v", got)
	}

	// un commit che non tocca i file visti lascia tutto com'è
	e.git.setSince("")
	e.git.setBranch(e.repo, "feat", sha(5001))
	if got := e.viewedOf("carol", n); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("carol dopo un commit che non tocca i suoi file = %v", got)
	}

	// una rinomina di b.txt (anche solo del percorso vecchio) lo riapre
	e.git.setSince(`[{"path":"c.txt","oldPath":"b.txt","status":"renamed","additions":0,"deletions":0,"binary":false,"truncated":false}]`)
	e.git.setBranch(e.repo, "feat", sha(5002))
	if got := e.viewedOf("carol", n); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("carol dopo la rinomina di b.txt = %v", got)
	}
}
