//go:build integration

package httpserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

func ownerUser(id, name string) identityclient.Owner {
	return identityclient.Owner{Type: "user", ID: uuid.MustParse(id), Name: name}
}

func join(s []string) string { return strings.Join(s, ",") }

func TestMilestones_CrudEAvanzamentoI7(t *testing.T) {
	e := newIssuesEnv(t)
	e.labelsRepo("app")

	e.want(e.do(http.MethodPost, "/repos/alice/app/milestones", "carol", `{"title":"v1"}`), http.StatusForbidden, "forbidden")
	rec := e.do(http.MethodPost, "/repos/alice/app/milestones", "bob", `{"title":"v1","description":"d","dueOn":"2027-01-31"}`)
	e.want(rec, http.StatusCreated, "")
	m := decodeInto[openapi.Milestone](t, rec)
	if m.Number != 1 || m.DueOn == nil || m.DueOn.Format("2006-01-02") != "2027-01-31" || m.State != "open" {
		t.Fatalf("milestone = %+v", m)
	}
	e.want(e.do(http.MethodPost, "/repos/alice/app/milestones", "bob", `{"title":"V1"}`), http.StatusConflict, "already_exists")
	e.want(e.do(http.MethodPost, "/repos/alice/app/milestones", "bob", `{"title":""}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodGet, "/repos/alice/app/milestones/1", "carol", ""), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/app/milestones/1", "carol", `{"title":"x"}`), http.StatusForbidden, "forbidden")

	// Avanzamento: 1 aperta, 1 completed, 1 not_planned e 1 duplicate (esclusi).
	set := func(n int64) {
		e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/milestone", n), "bob", `{"milestone":1}`), http.StatusOK, "")
	}
	open := e.open("app", "carol", "aperta")
	done := e.open("app", "carol", "fatta")
	np := e.open("app", "carol", "non pianificata")
	dup := e.open("app", "carol", "doppia")
	for _, n := range []int64{open, done, np, dup} {
		set(n)
	}
	cl := func(n int64, body string) {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", n), "bob", body), http.StatusOK, "")
	}
	cl(done, ``)
	cl(np, `{"reason":"not_planned"}`)
	cl(dup, fmt.Sprintf(`{"reason":"duplicate","duplicateOf":%d}`, open))
	m = decodeInto[openapi.Milestone](t, e.do(http.MethodGet, "/repos/alice/app/milestones/1", "carol", ""))
	if m.OpenIssues != 1 || m.ClosedIssues != 1 {
		t.Fatalf("avanzamento = %d aperte, %d completed; volute 1 e 1", m.OpenIssues, m.ClosedIssues)
	}

	m = decodeInto[openapi.Milestone](t, e.do(http.MethodPatch, "/repos/alice/app/milestones/1", "bob", `{"state":"closed","dueOn":null}`))
	if m.State != "closed" || m.ClosedAt == nil || m.DueOn != nil {
		t.Fatalf("chiusa = %+v", m)
	}
	l := decodeInto[openapi.MilestoneList](t, e.do(http.MethodGet, "/repos/alice/app/milestones?state=all", "carol", ""))
	if l.Total != 1 {
		t.Fatalf("elenco = %+v", l)
	}
	// Al massimo una milestone per issue: impostarne un'altra sostituisce.
	e.want(e.do(http.MethodPost, "/repos/alice/app/milestones", "bob", `{"title":"v2"}`), http.StatusCreated, "")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/milestone", open), "bob", `{"milestone":2}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/milestone", open), "bob", `{"milestone":99}`), http.StatusUnprocessableEntity, "validation_failed")
	if got := fmt.Sprint(eventTypes(e.events("app", open, "bob"))); got != "[opened milestoned demilestoned milestoned]" {
		t.Fatalf("cronologia = %s", got)
	}

	e.want(e.do(http.MethodDelete, "/repos/alice/app/milestones/1", "carol", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, "/repos/alice/app/milestones/1", "bob", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, "/repos/alice/app/milestones/1", "bob", ""), http.StatusNotFound, "not_found")
	x := e.issue(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/issues/%d", done), "bob", ""))
	if x.Milestone != nil {
		t.Fatalf("la issue ha ancora la milestone: %+v", x.Milestone)
	}
	if got := fmt.Sprint(eventTypes(e.events("app", done, "bob"))); got != "[opened milestoned closed demilestoned]" {
		t.Fatalf("cronologia = %s", got)
	}
}
