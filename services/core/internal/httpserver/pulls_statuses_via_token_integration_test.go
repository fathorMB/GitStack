//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// GIT-303 (v1.1): viaToken sugli stati dei commit. Uno stato scritto con un
// token porta viaToken (id e nome), uno scritto da una sessione no; by_agent
// registra se l'autore è un account agent.

func TestStatuses_ViaToken_GIT303(t *testing.T) {
	e := newMergeEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	users["bot"] = botID
	e.id.grantWrite(repoID, "bot")
	e.id.fakeIdentity.owners["bot"] = ownerUser(botID, "bot")
	e.branch(repoID, "feat")
	head := e.git.of(repoID)["feat"]
	tokenID := uuid.New()
	path := fmt.Sprintf("/repos/alice/app/commits/%s/statuses", head)

	// Con una sessione: niente viaToken.
	rec := e.do(http.MethodPost, path, "bob", `{"state":"success","context":"ci/session"}`)
	e.want(rec, http.StatusCreated, "")
	var viaSession openapi.CommitStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &viaSession); err != nil {
		t.Fatal(err)
	}
	if viaSession.ViaToken != nil {
		t.Fatalf("uno stato da sessione non ha viaToken: %+v", viaSession.ViaToken)
	}

	// Con un token: viaToken con id e nome.
	rec = e.doWithToken(http.MethodPost, path, "bob", `{"state":"pending","context":"ci/token"}`, tokenID, "ci-runner")
	e.want(rec, http.StatusCreated, "")
	var viaTok openapi.CommitStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &viaTok); err != nil {
		t.Fatal(err)
	}
	if viaTok.ViaToken == nil || viaTok.ViaToken.Name != "ci-runner" || uuid.UUID(viaTok.ViaToken.Id) != tokenID {
		t.Fatalf("viaToken = %+v, voluto ci-runner/%s", viaTok.ViaToken, tokenID)
	}

	// Elenco e stato combinato.
	rec = e.do(http.MethodGet, path, "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.CommitStatusList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	check := func(where string, items []openapi.CommitStatus) {
		t.Helper()
		if len(items) != 2 {
			t.Fatalf("%s: %d stati, voluti 2", where, len(items))
		}
		for _, s := range items {
			switch s.Context {
			case "ci/token":
				if s.ViaToken == nil || s.ViaToken.Name != "ci-runner" || uuid.UUID(s.ViaToken.Id) != tokenID {
					t.Fatalf("%s: ci/token viaToken = %+v", where, s.ViaToken)
				}
			case "ci/session":
				if s.ViaToken != nil {
					t.Fatalf("%s: ci/session viaToken = %+v", where, s.ViaToken)
				}
			default:
				t.Fatalf("%s: contesto inatteso %q", where, s.Context)
			}
		}
	}
	check("elenco", l.Items)
	check("combinato", e.combined("app", "carol", head).Statuses)

	// Lo stesso contesto riscritto da una sessione perde il token (una riga sola).
	e.want(e.do(http.MethodPost, path, "bob", `{"state":"success","context":"ci/token"}`), http.StatusCreated, "")
	for _, s := range e.combined("app", "carol", head).Statuses {
		if s.ViaToken != nil {
			t.Fatalf("dopo la riscrittura da sessione: %s ha viaToken %+v", s.Context, s.ViaToken)
		}
	}

	// by_agent: false per una persona, true per un agente.
	e.want(e.do(http.MethodPost, path, "bot", `{"state":"success","context":"ci/bot"}`), http.StatusCreated, "")
	byAgent := map[string]bool{}
	rows, err := e.pool.Query(context.Background(), `SELECT context, by_agent FROM core.commit_statuses WHERE repo_id = $1`, repoID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var a bool
		if err := rows.Scan(&c, &a); err != nil {
			t.Fatal(err)
		}
		byAgent[c] = a
	}
	if byAgent["ci/bot"] != true || byAgent["ci/session"] != false || len(byAgent) != 3 {
		t.Fatalf("by_agent = %v", byAgent)
	}
}
