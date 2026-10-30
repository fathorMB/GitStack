//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-456 (v3-c, H1): interruttore d'istanza delle app pubbliche e modo `public` per app.

func publicSettings(on bool) string {
	v := "false"
	if on {
		v = "true"
	}
	return `{"publicAppsEnabled":` + v + `,"signedImagesOnly":false,"restricted":false,"maxPerApp":{"cpuMillis":4000,"memoryBytes":8589934592},"logRetention":{"days":7,"maxBytes":1073741824}}`
}

type accessEventRow struct {
	kind, newValue string
	oldValue       *string
	actor          map[string]any
	hasApp         bool
}

func (e *appsEnv) accessEvents() []accessEventRow {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT kind, old_value, new_value, actor, app_id IS NOT NULL FROM core.app_access_events ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []accessEventRow
	for rows.Next() {
		var r accessEventRow
		var raw []byte
		if err := rows.Scan(&r.kind, &r.oldValue, &r.newValue, &raw, &r.hasApp); err != nil {
			e.t.Fatal(err)
		}
		if raw != nil {
			if err := json.Unmarshal(raw, &r.actor); err != nil {
				e.t.Fatal(err)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *appsEnv) accessOf(path, user string) openapi.AppAccess {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var acc openapi.AppAccess
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil {
		e.t.Fatal(err)
	}
	return acc
}

func TestAppsPublic_InterruttoreEScelta(t *testing.T) {
	e := newAppsEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	path := "/repos/alice/handbook/apps/web/access"
	public := `{"mode":"public"}`

	// Spento di default: la GET lo mostra e `public` è rifiutato anche all'admin dell'app.
	rec := e.do(http.MethodGet, "/admin/apps/settings", "carol", "")
	e.want(rec, http.StatusOK, "")
	var st openapi.AppsInstanceSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.PublicAppsEnabled {
		t.Fatalf("di default spento: %v %s", err, rec.Body.String())
	}
	if e.accessOf(path, "alice").PublicAppsEnabled {
		t.Fatal("l'accesso dell'app deve dire che le pubbliche sono spente")
	}
	e.want(e.do(http.MethodPut, path, "alice", public), http.StatusUnprocessableEntity, "public_apps_disabled")

	// L'interruttore lo cambia solo l'amministratore dell'installazione.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "alice", publicSettings(true)), http.StatusForbidden, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "bob", publicSettings(true)), http.StatusForbidden, "")
	if len(e.accessEvents()) != 0 {
		t.Fatal("un rifiuto non scrive nello storico")
	}
	rec = e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true))
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.PublicAppsEnabled {
		t.Fatalf("acceso: %v %s", err, rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/admin/apps/settings", "carol", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.PublicAppsEnabled {
		t.Fatalf("l'interruttore deve restare salvato: %v %s", err, rec.Body.String())
	}

	// Acceso: con write non basta, serve admin sull'app.
	e.want(e.do(http.MethodPut, path, "bob", public), http.StatusForbidden, "forbidden")
	if a := e.app(e.do(http.MethodGet, "/repos/alice/handbook/apps/web", "alice", "")); a.Access != openapi.AppAccessModeLogin {
		t.Fatalf("dopo i rifiuti l'app resta dietro il login: %s", a.Access)
	}
	rec = e.do(http.MethodPut, path, "alice", public)
	e.want(rec, http.StatusOK, "")
	var acc openapi.AppAccess
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || acc.Mode != openapi.AppAccessModePublic || !acc.PublicAppsEnabled {
		t.Fatalf("public: %v %s", err, rec.Body.String())
	}
	if acc.ChangedBy == nil || acc.ChangedBy.Username != "alice" || acc.ChangedAt == nil {
		t.Fatalf("changedBy/changedAt: %s", rec.Body.String())
	}
	// Non crea una versione.
	if l := e.versions("/repos/alice/handbook/apps/web/versions", "alice"); len(l.Items) != 1 {
		t.Fatalf("l'accesso non crea versioni: %d", len(l.Items))
	}

	// Spento: il modo scelto resta salvato, ma l'amministratore ha tolto l'esposizione a tutte le app.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(false)), http.StatusOK, "")
	acc = e.accessOf(path, "alice")
	if acc.Mode != openapi.AppAccessModePublic || acc.PublicAppsEnabled {
		t.Fatalf("spento: il modo resta, l'interruttore no: %+v", acc)
	}
	if a := e.app(e.do(http.MethodGet, "/repos/alice/handbook/apps/web", "alice", "")); a.Access != openapi.AppAccessModePublic {
		t.Fatalf("il modo scelto resta salvato: %s", a.Access)
	}
	// Spento di nuovo: nessuna nuova scelta di `public`; si può tornare al login.
	e.want(e.do(http.MethodPut, path, "alice", public), http.StatusUnprocessableEntity, "public_apps_disabled")

	// Riaccensione: torna valido senza toccare l'app.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	acc = e.accessOf(path, "alice")
	if acc.Mode != openapi.AppAccessModePublic || !acc.PublicAppsEnabled {
		t.Fatalf("riacceso: %+v", acc)
	}
	// Un PUT delle impostazioni con lo stesso valore non scrive eventi.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")

	// Storico con l'attore: acceso, scelta public, spento, acceso (nessun duplicato).
	ev := e.accessEvents()
	want := []struct{ kind, old, new, user string }{
		{"public_apps_enabled", "false", "true", "carol"},
		{"app_access", "login", "public", "alice"},
		{"public_apps_enabled", "true", "false", "carol"},
		{"public_apps_enabled", "false", "true", "carol"},
	}
	if len(ev) != len(want) {
		t.Fatalf("eventi: %+v", ev)
	}
	for i, w := range want {
		g := ev[i]
		if g.kind != w.kind || g.oldValue == nil || *g.oldValue != w.old || g.newValue != w.new || g.hasApp != (w.kind == "app_access") ||
			g.actor == nil || g.actor["username"] != w.user || g.actor["type"] != "human" || !strings.Contains(g.actor["id"].(string), "-") {
			t.Fatalf("evento %d: %+v, atteso %+v", i, g, w)
		}
	}
}

// Un agente con admin sull'app o sull'istanza compare nello storico con tipo `agent`.
func TestAppsPublic_StoricoDiUnAgente(t *testing.T) {
	e := newAppsEnv(t)
	e.registerBot()
	e.id.sysAdmin[users["bot"]] = true
	repoID := e.repo("handbook", true)
	e.id.mu.Lock()
	e.id.grants[repoID] = append(e.id.grants[repoID], users["bot"])
	e.id.mu.Unlock()
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "bot", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "bot", `{"mode":"public"}`), http.StatusOK, "")
	ev := e.accessEvents()
	if len(ev) != 2 {
		t.Fatalf("eventi: %+v", ev)
	}
	for _, g := range ev {
		if g.actor == nil || g.actor["username"] != "bot" || g.actor["type"] != "agent" {
			t.Fatalf("attore: %+v", g)
		}
	}
}
