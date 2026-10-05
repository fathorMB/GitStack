//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// GIT-125 (M-05): «via token» (mockup 13). Il token con cui il gateway ha
// autenticato il chiamante, firmato negli header X-Gitstack-Token-*, viene
// salvato alla creazione di issue e commenti e restituito come viaToken.

// doWithToken è e.do per un chiamante autenticato con un token.
func (e *issuesEnv) doWithToken(method, path, user, body string, tokenID uuid.UUID, tokenName string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{
		UserID: users[user], Username: user, Scopes: []string{"read:resource", "write:resource"},
		TokenID: tokenID.String(), TokenName: tokenName,
	}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func TestIssueViaToken_CommentoEIssue(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	tokenID := uuid.New()

	// Issue aperta con una sessione e commento con un token (bob).
	n := e.open("app", "bob", "con sessione")
	path := "/repos/alice/app/issues/" + strconv.FormatInt(n, 10)
	viaSession := e.comment(path, "bob", "da sessione")
	if viaSession.ViaToken != nil {
		t.Fatalf("un commento da sessione non ha viaToken: %+v", viaSession.ViaToken)
	}

	rec := e.doWithToken(http.MethodPost, path+"/comments", "bob", `{"body":"da token"}`, tokenID, "ci-runner")
	e.want(rec, http.StatusCreated, "")
	var viaTok openapi.IssueComment
	if err := json.Unmarshal(rec.Body.Bytes(), &viaTok); err != nil {
		t.Fatal(err)
	}
	if viaTok.ViaToken == nil || viaTok.ViaToken.Name != "ci-runner" || uuid.UUID(viaTok.ViaToken.Id) != tokenID {
		t.Fatalf("viaToken = %+v, voluto ci-runner/%s", viaTok.ViaToken, tokenID)
	}

	t.Run("elenco dei commenti", func(t *testing.T) {
		l := e.comments(path, "alice")
		if len(l.Items) != 2 {
			t.Fatalf("commenti = %d", len(l.Items))
		}
		for _, c := range l.Items {
			switch c.Body {
			case "da sessione":
				if c.ViaToken != nil {
					t.Errorf("sessione con viaToken: %+v", c.ViaToken)
				}
			case "da token":
				if c.ViaToken == nil || c.ViaToken.Name != "ci-runner" {
					t.Errorf("token senza viaToken: %+v", c.ViaToken)
				}
			}
		}
	})

	t.Run("issue creata con un token", func(t *testing.T) {
		rec := e.doWithToken(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"da agente","body":"x"}`, tokenID, "ci-runner")
		e.want(rec, http.StatusCreated, "")
		var is openapi.Issue
		if err := json.Unmarshal(rec.Body.Bytes(), &is); err != nil {
			t.Fatal(err)
		}
		if is.ViaToken == nil || is.ViaToken.Name != "ci-runner" || uuid.UUID(is.ViaToken.Id) != tokenID {
			t.Fatalf("viaToken = %+v", is.ViaToken)
		}
		get := e.do(http.MethodGet, "/repos/alice/app/issues/"+strconv.FormatInt(is.Number, 10), "alice", "")
		e.want(get, http.StatusOK, "")
		var again openapi.Issue
		if err := json.Unmarshal(get.Body.Bytes(), &again); err != nil {
			t.Fatal(err)
		}
		if again.ViaToken == nil || again.ViaToken.Name != "ci-runner" {
			t.Fatalf("viaToken in lettura = %+v", again.ViaToken)
		}
		// L'issue aperta con la sessione non ce l'ha.
		sess := e.do(http.MethodGet, "/repos/alice/app/issues/"+strconv.FormatInt(n, 10), "alice", "")
		var plain openapi.Issue
		if err := json.Unmarshal(sess.Body.Bytes(), &plain); err != nil {
			t.Fatal(err)
		}
		if plain.ViaToken != nil {
			t.Errorf("issue da sessione con viaToken: %+v", plain.ViaToken)
		}
	})
}
