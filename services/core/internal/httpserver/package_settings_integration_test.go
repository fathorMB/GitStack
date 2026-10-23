//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// GIT-325 (RG2): GET/PATCH packages/settings in core. Solo l'admin del repo; i default sono
// quelli di RG2; il corpo si valida; il repo archiviato non si modifica; il servizio git le
// legge con la sua chiave e nessun altro.
func TestPackageSettings_PermessiDefaultEValidazione(t *testing.T) {
	e := newHookEnv(t)
	repoID := e.repo("app", true) // internal: bob e carol leggono
	e.hid.grantWrite(repoID, "bob")
	base := "/repos/alice/app/packages/settings"
	const body = `{"protectedTagPatterns":["v*","release-*"],"deleteUntaggedAfterDays":3,"includeInBackup":true,"keepLastTags":5,"deleteShaTagsAfterDays":30}`

	// Chi non è admin (anche con write) riceve 403 sia in lettura sia in modifica; non è più 501.
	e.want(e.do(http.MethodGet, base, "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPatch, base, "bob", body), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base, "carol", ""), http.StatusForbidden, "forbidden")
	// Un repo privato non si vede: 404.
	e.repo("riservato", false)
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/packages/settings", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/riservato/packages/settings", "carol", body), http.StatusNotFound, "")

	// Default di RG2.
	rec := e.do(http.MethodGet, base, "alice", "")
	e.want(rec, http.StatusOK, "")
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["deleteUntaggedAfterDays"] != float64(7) || got["includeInBackup"] != false || len(got["protectedTagPatterns"].([]any)) != 0 {
		t.Fatalf("default: %s", rec.Body.String())
	}
	if _, ok := got["keepLastTags"]; ok {
		t.Fatalf("keepLastTags nei default: %s", rec.Body.String())
	}

	// Modifica e rilettura.
	rec = e.do(http.MethodPatch, base, "alice", body)
	e.want(rec, http.StatusOK, "")
	rec = e.do(http.MethodGet, base, "alice", "")
	e.want(rec, http.StatusOK, "")
	for _, want := range []string{`"v*"`, `"release-*"`, `"deleteUntaggedAfterDays":3`, `"includeInBackup":true`, `"keepLastTags":5`, `"deleteShaTagsAfterDays":30`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("manca %s: %s", want, rec.Body.String())
		}
	}
	// Il PATCH sostituisce: i campi facoltativi assenti spengono la regola.
	e.want(e.do(http.MethodPatch, base, "alice", `{"protectedTagPatterns":[],"deleteUntaggedAfterDays":7,"includeInBackup":false}`), http.StatusOK, "")
	rec = e.do(http.MethodGet, base, "alice", "")
	if strings.Contains(rec.Body.String(), "keepLastTags") || strings.Contains(rec.Body.String(), "deleteShaTagsAfterDays") {
		t.Fatalf("regole non spente: %s", rec.Body.String())
	}

	// Validazione.
	for _, bad := range []string{
		`{"protectedTagPatterns":[],"deleteUntaggedAfterDays":0,"includeInBackup":false}`,
		`{"protectedTagPatterns":["a/b"],"deleteUntaggedAfterDays":7,"includeInBackup":false}`,
		`{"protectedTagPatterns":["["],"deleteUntaggedAfterDays":7,"includeInBackup":false}`,
		`{"protectedTagPatterns":["v*","v*"],"deleteUntaggedAfterDays":7,"includeInBackup":false}`,
		`{"protectedTagPatterns":[],"deleteUntaggedAfterDays":7,"includeInBackup":false,"keepLastTags":0}`,
		`{"protectedTagPatterns":[],"deleteUntaggedAfterDays":7,"includeInBackup":false,"deleteShaTagsAfterDays":-1}`,
	} {
		e.want(e.do(http.MethodPatch, base, "alice", bad), http.StatusUnprocessableEntity, "validation_failed")
	}
	e.want(e.do(http.MethodPatch, base, "alice", `{"protectedTagPatterns":[],"deleteUntaggedAfterDays":7,"includeInBackup":false,"altro":1}`), http.StatusBadRequest, "")

	// Repo archiviato: la lettura passa, la modifica è 409.
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoID)
	e.want(e.do(http.MethodGet, base, "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, base, "alice", body), http.StatusConflict, "archived")
}

func TestPackageSettings_RottaInternaSoloPerGit(t *testing.T) {
	e := newHookEnv(t)
	repoID := e.repo("app", true)
	e.repo("altro", true)
	other := e.repo("terzo", true)
	e.want(e.do(http.MethodPatch, "/repos/alice/app/packages/settings", "alice",
		`{"protectedTagPatterns":["v*"],"deleteUntaggedAfterDays":2,"includeInBackup":true,"keepLastTags":4}`), http.StatusOK, "")

	const gitSecret = "segreto-del-servizio-git"
	router := httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret, httpserver.WithGitSecret(gitSecret), httpserver.WithRepoIdentity(e.hid))
	call := func(secret, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		trust.Sign(req.Header, secret, trust.Identity{UserID: aliceID, Username: "registry"}, time.Now())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	rec := call(gitSecret, "/internal/registry/settings/"+repoID.String())
	e.want(rec, http.StatusOK, "")
	var got struct {
		Owner        string   `json:"owner"`
		Name         string   `json:"name"`
		Archived     bool     `json:"archived"`
		Protected    []string `json:"protectedTagPatterns"`
		Untagged     int      `json:"deleteUntaggedAfterDays"`
		Keep         *int     `json:"keepLastTags"`
		Backup       bool     `json:"includeInBackup"`
		OwnerRepoIDs []string `json:"ownerRepoIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Owner != "alice" || got.Name != "app" || got.Archived || len(got.Protected) != 1 || got.Untagged != 2 || got.Keep == nil || *got.Keep != 4 || !got.Backup {
		t.Fatalf("impostazioni: %s", rec.Body.String())
	}
	found := false
	for _, id := range got.OwnerRepoIDs {
		found = found || id == other.String()
	}
	if len(got.OwnerRepoIDs) != 3 || !found {
		t.Fatalf("repo dell'owner: %v", got.OwnerRepoIDs)
	}
	// Id sconosciuto o non valido: 404. Eliminato: 404.
	e.want(call(gitSecret, "/internal/registry/settings/00000000-0000-0000-0000-000000000001"), http.StatusNotFound, "")
	e.want(call(gitSecret, "/internal/registry/settings/non-un-uuid"), http.StatusNotFound, "")
	e.sql(`UPDATE core.repositories SET deleted_at = now() WHERE resource_id = $1`, other)
	e.want(call(gitSecret, "/internal/registry/settings/"+other.String()), http.StatusNotFound, "")
	// Il gateway (chiave di servizio generale) e un segreto sbagliato non passano.
	e.want(call(trustSecret, "/internal/registry/settings/"+repoID.String()), http.StatusForbidden, "forbidden")
	e.want(call("altro-segreto", "/internal/registry/settings/"+repoID.String()), http.StatusUnauthorized, "")
}
