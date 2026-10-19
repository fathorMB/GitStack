//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// GIT-264 (v1.1): API degli stati dei commit (PR9) e gate del merge con
// «Richiedi una Pull Request» (PR4): approvazioni minime, approvazioni annullate
// da un nuovo commit, contesti obbligatori mancanti o rossi.

func (e *mergeEnv) postStatus(repo, user, sha, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, fmt.Sprintf("/repos/alice/%s/commits/%s/statuses", repo, sha), user, body)
}

func (e *mergeEnv) combined(repo, user, sha string) openapi.CombinedCommitStatus {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/%s/commits/%s/status", repo, sha), user, "")
	e.want(rec, http.StatusOK, "")
	var c openapi.CombinedCommitStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatalf("stato non valido: %v: %s", err, rec.Body.String())
	}
	return c
}

func (e *mergeEnv) blockers(repo string, n int64) []string {
	e.t.Helper()
	var out []string
	for _, b := range e.mergeability(repo, n).Blockers {
		out = append(out, string(b.Code))
	}
	return out
}

func TestStatuses_ScritturaELetturaCombinata(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	head := e.git.of(repoID)["feat"]

	t.Run("senza_stati_none", func(t *testing.T) {
		c := e.combined("app", "carol", head)
		if c.State != "none" || c.TotalCount != 0 || c.Sha != head || len(c.Statuses) != 0 || len(c.RequiredContexts) != 0 {
			t.Fatalf("stato inatteso: %+v", c)
		}
	})

	t.Run("serve_write_e_i_dati_sono_validati", func(t *testing.T) {
		e.want(e.postStatus("app", "carol", head, `{"state":"success"}`), http.StatusForbidden, "forbidden")
		for name, body := range map[string]string{
			"stato_sconosciuto":  `{"state":"ok"}`,
			"contesto_troppo":    fmt.Sprintf(`{"state":"success","context":%q}`, strings.Repeat("c", 256)),
			"descrizione_troppo": fmt.Sprintf(`{"state":"success","description":%q}`, strings.Repeat("d", 141)),
			"url_non_http":       `{"state":"success","targetUrl":"javascript:alert(1)"}`,
			"url_ftp":            `{"state":"success","targetUrl":"ftp://example.com/log"}`,
		} {
			rec := e.postStatus("app", "bob", head, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status %d, atteso 422: %s", name, rec.Code, rec.Body.String())
			}
		}
		e.want(e.postStatus("app", "bob", head, `{"context":"ci"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.postStatus("app", "bob", strings.Repeat("f", 40), `{"state":"success"}`), http.StatusNotFound, "ref_not_found")
		e.want(e.postStatus("app", "bob", "zzzzzzzz", `{"state":"success"}`), http.StatusBadRequest, "")
		e.want(e.do(http.MethodGet, "/repos/alice/app/commits/"+strings.Repeat("f", 40)+"/status", "bob", ""), http.StatusNotFound, "ref_not_found")
		if c := e.combined("app", "carol", head); c.TotalCount != 0 {
			t.Fatalf("nessuno stato doveva essere scritto: %+v", c)
		}
	})

	t.Run("write_scrive_con_contesto_descrizione_e_link", func(t *testing.T) {
		rec := e.postStatus("app", "bob", head, `{"state":"pending","context":"ci/build","description":"in corso","targetUrl":"https://ci.example.com/run/1"}`)
		e.want(rec, http.StatusCreated, "")
		var cs openapi.CommitStatus
		_ = json.Unmarshal(rec.Body.Bytes(), &cs)
		if cs.Sha != head || cs.State != "pending" || cs.Context != "ci/build" || cs.Description == nil || *cs.Description != "in corso" ||
			cs.TargetUrl == nil || *cs.TargetUrl != "https://ci.example.com/run/1" || cs.Creator.Username != "bob" {
			t.Fatalf("stato inatteso: %+v", cs)
		}
	})

	t.Run("un_token_con_write_scrive_e_il_contesto_ha_un_default", func(t *testing.T) {
		rec := e.doWithToken(http.MethodPost, fmt.Sprintf("/repos/alice/app/commits/%s/statuses", head), "bob", `{"state":"success"}`, uuid.New(), "ci-runner")
		e.want(rec, http.StatusCreated, "")
		var cs openapi.CommitStatus
		_ = json.Unmarshal(rec.Body.Bytes(), &cs)
		if cs.Context != "default" {
			t.Fatalf("contesto = %q", cs.Context)
		}
	})

	t.Run("l_ultimo_stato_del_contesto_sostituisce_il_precedente", func(t *testing.T) {
		e.want(e.postStatus("app", "bob", head, `{"state":"failure","context":"ci/build"}`), http.StatusCreated, "")
		c := e.combined("app", "carol", head)
		if c.State != "failure" || c.TotalCount != 2 || len(c.Statuses) != 2 {
			t.Fatalf("combinato inatteso: %+v", c)
		}
		for _, s := range c.Statuses {
			if s.Context == "ci/build" && s.State != "failure" {
				t.Fatalf("ci/build = %s", s.State)
			}
		}
		e.want(e.postStatus("app", "alice", head, `{"state":"success","context":"ci/build"}`), http.StatusCreated, "")
		c = e.combined("app", "carol", head)
		if c.State != "success" || c.TotalCount != 2 {
			t.Fatalf("dopo il successo: %+v", c)
		}
		rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/commits/%s/statuses?perPage=1", head), "carol", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.CommitStatusList
		_ = json.Unmarshal(rec.Body.Bytes(), &l)
		if l.Total != 2 || len(l.Items) != 1 || l.PerPage != 1 || l.Items[0].Context != "ci/build" {
			t.Fatalf("elenco inatteso: %+v", l)
		}
	})

	t.Run("sintesi_error_pending_e_prefisso_di_sha", func(t *testing.T) {
		e.want(e.postStatus("app", "bob", head[:39], `{"state":"pending","context":"slow"}`), http.StatusCreated, "")
		if c := e.combined("app", "carol", head); c.State != "pending" || c.TotalCount != 3 {
			t.Fatalf("pending: %+v", c)
		}
		e.want(e.postStatus("app", "bob", head, `{"state":"error","context":"slow"}`), http.StatusCreated, "")
		if c := e.combined("app", "carol", head[:39]); c.State != "error" || c.Sha != head {
			t.Fatalf("error: %+v", c)
		}
	})

	t.Run("repo_non_leggibile_404_e_archiviato_409", func(t *testing.T) {
		sid := e.repo("secret", false)
		e.branch(sid, "feat")
		sh := e.git.of(sid)["feat"]
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/secret/commits/%s/status", sh), "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/secret/commits/%s/statuses", sh), "carol", ""), http.StatusNotFound, "not_found")
		e.want(e.postStatus("secret", "carol", sh, `{"state":"success"}`), http.StatusNotFound, "not_found")
		e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoID)
		e.want(e.postStatus("app", "bob", head, `{"state":"success","context":"nuovo"}`), http.StatusConflict, "archived")
		e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/commits/%s/status", head), "carol", ""), http.StatusOK, "")
	})
}

func TestMergeGate_ApprovazioniMinimeENuovoCommit_PR4(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "carol")
	e.branch(repoID, "feat")
	n := e.open("app", "bob", "feat")
	approve := func(user string) {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/reviews", n), user, `{"event":"approve"}`), http.StatusCreated, "")
	}

	// Impostazione spenta: nessuna approvazione richiesta.
	if got := e.blockers("app", n); len(got) != 0 {
		t.Fatalf("a impostazione spenta: blocchi %v", got)
	}

	e.want(e.settings("app", "alice", `{"requirePullRequest":true,"requiredApprovals":2}`), http.StatusOK, "")
	if got := e.blockers("app", n); !slices.Equal(got, []string{"approvals_required"}) {
		t.Fatalf("senza approvazioni: %v", got)
	}
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "approvals_required")

	approve("alice")
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "approvals_required")
	if m := e.mergeability("app", n); m.Approvals != 1 || m.RequiredApprovals != 2 || m.Mergeable {
		t.Fatalf("1 su 2: %+v", m)
	}

	approve("carol")
	if got := e.blockers("app", n); len(got) != 0 {
		t.Fatalf("con due approvazioni: blocchi %v", got)
	}

	// Un nuovo commit sul branch di origine annulla le approvazioni.
	e.git.setBranch(repoID, "feat", sha(777))
	if m := e.mergeability("app", n); m.Approvals != 0 || m.Mergeable || !slices.Equal(e.blockers("app", n), []string{"approvals_required"}) {
		t.Fatalf("dopo il nuovo commit: %+v", m)
	}
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "approvals_required")
	if e.mg.mergeCount() != 0 {
		t.Fatal("git non doveva essere chiamato")
	}

	approve("alice")
	approve("carol")
	// Una richiesta di modifiche blocca anche con le approvazioni.
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/reviews", n), "carol", `{"event":"request_changes","body":"no"}`), http.StatusCreated, "")
	got := e.blockers("app", n)
	if !slices.Contains(got, "changes_requested") {
		t.Fatalf("modifiche richieste: %v", got)
	}
	approve("carol")
	// Con le approvazioni sull'ultimo commit il merge passa.
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
}

func TestMergeGate_ContestiObbligatori_PR9(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	n := e.open("app", "bob", "feat")
	head := e.git.of(repoID)["feat"]

	rec := e.settings("app", "alice", `{"requiredStatusContexts":["ci/build","ci/test"]}`)
	e.want(rec, http.StatusOK, "")

	t.Run("mancanti_bloccano", func(t *testing.T) {
		m := e.mergeability("app", n)
		if m.Mergeable || len(m.Blockers) != 1 || m.Blockers[0].Code != "status_required" || m.Blockers[0].Contexts == nil ||
			!slices.Equal(*m.Blockers[0].Contexts, []string{"ci/build", "ci/test"}) {
			t.Fatalf("mergeability: %+v", m)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "status_required")
		c := e.combined("app", "carol", head)
		if len(c.RequiredContexts) != 2 || c.RequiredContexts[0].State != nil || c.RequiredContexts[0].Satisfied {
			t.Fatalf("requiredContexts: %+v", c.RequiredContexts)
		}
	})

	t.Run("pending_blocca_come_mancante", func(t *testing.T) {
		e.want(e.postStatus("app", "bob", head, `{"state":"success","context":"ci/build"}`), http.StatusCreated, "")
		e.want(e.postStatus("app", "bob", head, `{"state":"pending","context":"ci/test"}`), http.StatusCreated, "")
		if got := e.blockers("app", n); !slices.Equal(got, []string{"status_required"}) {
			t.Fatalf("blocchi = %v", got)
		}
	})

	t.Run("rosso_blocca_status_failed", func(t *testing.T) {
		e.want(e.postStatus("app", "bob", head, `{"state":"failure","context":"ci/test"}`), http.StatusCreated, "")
		m := e.mergeability("app", n)
		if len(m.Blockers) != 1 || m.Blockers[0].Code != "status_failed" || !slices.Equal(*m.Blockers[0].Contexts, []string{"ci/test"}) {
			t.Fatalf("mergeability: %+v", m)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "status_failed")
		e.want(e.postStatus("app", "bob", head, `{"state":"error","context":"ci/test"}`), http.StatusCreated, "")
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "status_failed")
		if e.mg.mergeCount() != 0 {
			t.Fatal("git non doveva essere chiamato")
		}
	})

	t.Run("uno_stato_su_un_altro_sha_non_conta_e_il_successo_sblocca", func(t *testing.T) {
		e.want(e.postStatus("app", "bob", head, `{"state":"success","context":"ci/test"}`), http.StatusCreated, "")
		// Un nuovo commit: gli stati dello sha vecchio non valgono.
		e.git.setBranch(repoID, "feat", sha(888))
		if got := e.blockers("app", n); !slices.Equal(got, []string{"status_required"}) {
			t.Fatalf("dopo il nuovo commit: %v", got)
		}
		newHead := e.git.of(repoID)["feat"]
		e.want(e.postStatus("app", "bob", newHead, `{"state":"success","context":"ci/build"}`), http.StatusCreated, "")
		e.want(e.postStatus("app", "bob", newHead, `{"state":"success","context":"ci/test"}`), http.StatusCreated, "")
		if got := e.blockers("app", n); len(got) != 0 {
			t.Fatalf("con tutti i success: %v", got)
		}
		// Il campo statusState della PR segue lo sha attuale.
		prRec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d", n), "carol", "")
		e.want(prRec, http.StatusOK, "")
		if x := e.pr(prRec); x.StatusState == nil || *x.StatusState != "success" {
			t.Fatalf("statusState = %v", x.StatusState)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
	})
}

// MP: un repo con un mirror in pull attivo è in sola lettura. Con
// «Richiedi una Pull Request» accesa il merge di una PR è rifiutato con un
// messaggio chiaro (read_only_mirror); il fetch del mirror non passa dalle
// regole di push. Il repo espone requirePullRequest, che legge il servizio git.
func TestMergeGate_MirrorInPullERepoRequirePullRequest(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("mirrored", true)
	e.id.grantWrite(repoID, "bob")
	e.branch(repoID, "feat")
	n := e.open("mirrored", "bob", "feat")

	getRepo := func() openapi.Repository {
		rec := e.do(http.MethodGet, "/repos/alice/mirrored", "carol", "")
		e.want(rec, http.StatusOK, "")
		var r openapi.Repository
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := getRepo(); r.RequirePullRequest != nil && *r.RequirePullRequest {
		t.Fatalf("a impostazione spenta: %+v", r.RequirePullRequest)
	}
	e.want(e.settings("mirrored", "alice", `{"requirePullRequest":true,"requiredApprovals":1}`), http.StatusOK, "")
	if r := getRepo(); r.RequirePullRequest == nil || !*r.RequirePullRequest {
		t.Fatal("requirePullRequest doveva essere true: lo legge il servizio git")
	}
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/mirrored/pulls/%d/reviews", n), "alice", `{"event":"approve"}`), http.StatusCreated, "")
	e.sql(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, state, created_by) VALUES ($1, $2, 'https://example.com/x.git', 'in_sync', $3)`, uuid.New(), repoID, uuid.MustParse(aliceID))
	rec := e.merge("mirrored", n, "bob", `{"method":"squash"}`)
	e.want(rec, http.StatusConflict, "read_only_mirror")
	if !strings.Contains(rec.Body.String(), "mirror in pull") {
		t.Fatalf("messaggio poco chiaro: %s", rec.Body.String())
	}
	if e.mg.mergeCount() != 0 {
		t.Fatal("git non doveva essere chiamato")
	}
}
