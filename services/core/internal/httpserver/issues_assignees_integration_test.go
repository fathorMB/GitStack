//go:build integration

package httpserver_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

func TestAssignees_LimiteEPermessiI6(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.labelsRepo("app")
	e.id.grantWrite(repoID, "bot")
	n := e.open("app", "carol", "da assegnare")
	p := fmt.Sprintf("/repos/alice/app/issues/%d/assignees", n)

	t.Run("senza_write_403", func(t *testing.T) {
		e.want(e.do(http.MethodPut, p, "carol", `{"assignees":["carol"]}`), http.StatusForbidden, "forbidden")
	})
	t.Run("assegnatario_senza_write_422", func(t *testing.T) {
		e.want(e.do(http.MethodPut, p, "bob", `{"assignees":["carol"]}`), http.StatusUnprocessableEntity, "assignees")
		e.want(e.do(http.MethodPut, p, "bob", `{"assignees":["nessuno"]}`), http.StatusUnprocessableEntity, "assignees")
	})
	t.Run("autoassegnazione_di_un_agente_con_write", func(t *testing.T) {
		x := e.issue(e.do(http.MethodPut, p, "bot", `{"assignees":["bot"]}`))
		if len(x.Assignees) != 1 || x.Assignees[0].Username != "bot" || x.Assignees[0].Kind != openapi.IssueUserKindAgent {
			t.Fatalf("assegnatari = %+v", x.Assignees)
		}
	})
	t.Run("sostituzione_ed_eventi", func(t *testing.T) {
		x := e.issue(e.do(http.MethodPut, p, "bob", `{"assignees":["alice","bob"]}`))
		if len(x.Assignees) != 2 {
			t.Fatalf("assegnatari = %+v", x.Assignees)
		}
		e.want(e.do(http.MethodPut, p, "bob", `{"assignees":[]}`), http.StatusOK, "")
		want := "[opened assigned assigned assigned unassigned unassigned unassigned]"
		if got := fmt.Sprint(eventTypes(e.events("app", n, "bob"))); got != want {
			t.Fatalf("cronologia = %s, voluta %s", got, want)
		}
	})
	t.Run("undicesimo_assegnatario_422", func(t *testing.T) {
		var names []string
		for i := 0; i < 11; i++ {
			name := fmt.Sprintf("bot%d", i)
			id := uuid.NewString()
			users[name] = id
			e.id.owners[name] = ownerUser(id, name)
			e.id.grantWrite(repoID, name)
			names = append(names, fmt.Sprintf("%q", name))
		}
		body := func(k int) string { return fmt.Sprintf(`{"assignees":[%s]}`, join(names[:k])) }
		e.want(e.do(http.MethodPut, p, "bob", body(11)), http.StatusUnprocessableEntity, "assignees")
		x := e.issue(e.do(http.MethodPut, p, "bob", body(10)))
		if len(x.Assignees) != 10 {
			t.Fatalf("assegnatari = %d, voluti 10", len(x.Assignees))
		}
	})
	t.Run("alla_creazione", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"t","assignees":["carol"]}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"t","assignees":["bob"]}`)
		e.want(rec, http.StatusCreated, "")
		if x := e.issue(rec); len(x.Assignees) != 1 || x.Assignees[0].Username != "bob" {
			t.Fatalf("assegnatari = %+v", x.Assignees)
		}
	})
}
