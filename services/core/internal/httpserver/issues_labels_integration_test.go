//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// M-05/E (GIT-105): etichette (I5), assegnatari (I6), milestone (I7).
// alice = admin, bob = write, carol = read (repo internal).

const botID = "eeeeeeee-0000-0000-0000-000000000006"

func (e *issuesEnv) labelsRepo(name string) uuid.UUID {
	e.t.Helper()
	users["bot"] = botID
	e.id.owners["bot"] = identityclient.Owner{Type: "user", ID: uuid.MustParse(botID), Name: "bot"}
	repoID := e.repo(name, true)
	e.id.grantWrite(repoID, "bob")
	return repoID
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, rec.Body.String())
	}
	return v
}

func TestLabels_PredefiniteECrudI5(t *testing.T) {
	e := newIssuesEnv(t)
	e.labelsRepo("app")

	t.Run("predefinite_alla_creazione", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/repos/alice/app/labels", "carol", "")
		e.want(rec, http.StatusOK, "")
		l := decodeInto[openapi.LabelList](t, rec)
		if l.Total != 8 || len(l.Items) != 8 {
			t.Fatalf("etichette predefinite = %d, volute 8", l.Total)
		}
	})
	t.Run("senza_predefinite_con_opzione_spenta", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"nolabels","defaultLabels":false}`), http.StatusCreated, "")
		l := decodeInto[openapi.LabelList](t, e.do(http.MethodGet, "/repos/alice/nolabels/labels", "alice", ""))
		if l.Total != 0 {
			t.Fatalf("con defaultLabels=false: %d etichette", l.Total)
		}
	})
	t.Run("crud_con_write_e_rifiuto_con_read", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/labels", "carol", `{"name":"x","color":"ff0000"}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPost, "/repos/alice/app/labels", "bob", `{"name":"good idea","color":"FF0000","description":"d"}`)
		e.want(rec, http.StatusCreated, "")
		if l := decodeInto[openapi.Label](t, rec); l.Color != "ff0000" || l.Name != "good idea" {
			t.Fatalf("etichetta = %+v", l)
		}
		e.want(e.do(http.MethodPost, "/repos/alice/app/labels", "bob", `{"name":"GOOD IDEA","color":"ff0000"}`), http.StatusConflict, "already_exists")
		e.want(e.do(http.MethodPost, "/repos/alice/app/labels", "bob", `{"name":"a/b","color":"ff0000"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, "/repos/alice/app/labels", "bob", `{"name":"ok","color":"red"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodGet, "/repos/alice/app/labels/good%20idea", "carol", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPatch, "/repos/alice/app/labels/good%20idea", "carol", `{"color":"00ff00"}`), http.StatusForbidden, "forbidden")
		rec = e.do(http.MethodPatch, "/repos/alice/app/labels/good%20idea", "bob", `{"name":"great","color":"00ff00"}`)
		e.want(rec, http.StatusOK, "")
		if l := decodeInto[openapi.Label](t, rec); l.Name != "great" || l.Color != "00ff00" {
			t.Fatalf("modificata = %+v", l)
		}
		e.want(e.do(http.MethodDelete, "/repos/alice/app/labels/great", "carol", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodDelete, "/repos/alice/app/labels/great", "bob", ""), http.StatusNoContent, "")
		e.want(e.do(http.MethodGet, "/repos/alice/app/labels/great", "bob", ""), http.StatusNotFound, "not_found")
	})
	t.Run("etichette_sulla_issue_e_cancellazione", func(t *testing.T) {
		n := e.open("app", "carol", "con etichette")
		p := fmt.Sprintf("/repos/alice/app/issues/%d/labels", n)
		e.want(e.do(http.MethodPut, p, "carol", `{"labels":["bug"]}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, p, "bob", `{"labels":["nope"]}`), http.StatusUnprocessableEntity, "validation_failed")
		x := e.issue(e.do(http.MethodPut, p, "bob", `{"labels":["bug","question"]}`))
		if len(x.Labels) != 2 {
			t.Fatalf("etichette = %+v", x.Labels)
		}
		x = e.issue(e.do(http.MethodPut, p, "bob", `{"labels":["bug"]}`))
		if len(x.Labels) != 1 {
			t.Fatalf("etichette dopo la sostituzione = %+v", x.Labels)
		}
		e.want(e.do(http.MethodDelete, "/repos/alice/app/labels/bug", "bob", ""), http.StatusNoContent, "")
		evs := eventTypes(e.events("app", n, "bob"))
		want := []string{"opened", "labeled", "labeled", "unlabeled", "unlabeled"}
		if fmt.Sprint(evs) != fmt.Sprint(want) {
			t.Fatalf("cronologia = %v, voluta %v", evs, want)
		}
	})
}
