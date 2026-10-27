package cigateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/apppull"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/jackc/pgx/v5"
)

// appPullAccess risponde per il token di pull di un'app (GIT-425): sola lettura (packages: read)
// sul registry del repo dell'app, e nessun altro repo (404, come per il token del job). Il
// gateway esposto fuori dal cluster non lo accetta: il token vive solo nel cluster.
func (g *gateway) appPullAccess(w http.ResponseWriter, r *http.Request, token string) {
	unauthorized := func() {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gitstack-ci"`)
		http.Error(w, "token non valido", http.StatusUnauthorized)
	}
	if g.ExternalOnly {
		unauthorized()
		return
	}
	appID, err := apppull.Verify(g.AppPullKeys, token, g.Now())
	if err != nil {
		unauthorized()
		return
	}
	owner := strings.ToLower(r.URL.Query().Get("owner"))
	name := strings.ToLower(r.URL.Query().Get("repo"))
	var out RegistryAccess
	err = g.Pool.QueryRow(r.Context(), `
		SELECT rp.resource_id::text, rp.owner_name, rp.name, rp.archived_at IS NOT NULL
		FROM core.apps a
		JOIN core.repositories rp ON rp.resource_id = a.repo_id
		WHERE a.id = $1 AND rp.deleted_at IS NULL AND lower(rp.owner_name) = $2 AND lower(rp.name) = $3`,
		appID, owner, name).Scan(&out.RepoID, &out.Owner, &out.Name, &out.Archived)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: repo dell'app non leggibile", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	out.Workflow, out.Job, out.Packages = "app", "pull", ci.LevelRead
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// RegistryAccessPath è l'endpoint con cui il registry (servizio git) trasforma il
// token del job in ciò che quel job può fare sul registry del suo repo (RG1).
const RegistryAccessPath = "/ci/v1/registry-access"

// RegistryAccess è la risposta di RegistryAccessPath.
type RegistryAccess struct {
	RepoID string `json:"repoId"`
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	// Workflow e Job identificano chi pubblica con il token del job.
	Workflow string `json:"workflow"`
	Job      string `json:"job"`
	// Archived: il repo è archiviato (R10): si scarica, non si pubblica.
	Archived bool `json:"archived"`
	// Packages è il livello `packages` del token del job (CI4): none, read o write.
	// Il token del job non cancella mai: l'eliminazione resta ad admin.
	Packages string `json:"packages"`
}

// registryAccess risponde per il repo owner/repo (query `owner`, `repo`) solo se è il
// repo del job a cui appartiene il token: ogni altro repo, anche esistente, è 404, così il
// token non rivela nulla degli altri repo. Token non valido: 401.
func (g *gateway) registryAccess(w http.ResponseWriter, r *http.Request) {
	if tok := bearer(r.Header); apppull.Is(tok) {
		g.appPullAccess(w, r, tok)
		return
	}
	a, err := g.resolveJob(r.Context(), bearer(r.Header))
	if errors.Is(err, ci.ErrJobToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gitstack-ci"`)
		http.Error(w, "token del job non valido", http.StatusUnauthorized)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: verifica del token non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	owner := strings.ToLower(r.URL.Query().Get("owner"))
	name := strings.ToLower(r.URL.Query().Get("repo"))
	var out RegistryAccess
	var perms []byte
	err = g.Pool.QueryRow(r.Context(), `
		SELECT rp.resource_id::text, rp.owner_name, rp.name, r.workflow_name, j.name,
		       rp.archived_at IS NOT NULL, j.permissions
		FROM core.ci_jobs j
		JOIN core.ci_runs r ON r.id = j.run_id
		JOIN core.repositories rp ON rp.resource_id = r.repo_id
		WHERE j.id = $1 AND rp.deleted_at IS NULL AND lower(rp.owner_name) = $2 AND lower(rp.name) = $3`,
		a.JobID, owner, name).Scan(&out.RepoID, &out.Owner, &out.Name, &out.Workflow, &out.Job, &out.Archived, &perms)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: repo del job non leggibile", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	var m map[string]string
	_ = json.Unmarshal(perms, &m)
	out.Packages = m["packages"]
	if !ci.ValidLevel(out.Packages) {
		out.Packages = ci.LevelNone
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}
