package httpserver

// Token del job sulle rotte delle release (GIT-372, REL1): un workflow con
// `permissions: contents: write` crea la release del proprio repo e ne carica
// gli asset, e nient'altro.
//
// Il gateway ammette il token del job (gsj_) solo sulle operazioni con
// x-job-token (createRelease, uploadReleaseAsset) e firma l'identità
// «system:ci-job»; il token arriva in X-Gitstack-Job-Token. Core lo verifica
// da sé contro il database dei job: deve valere, il repo dell'URL deve essere
// quello del job e il livello `contents` salvato per il job deve essere write.
// Una seconda barriera (jobGuard) rifiuta l'identità del job su ogni rotta che
// non sia una delle due, anche se il gateway sbagliasse.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// isJobIdentity dice se l'identità firmata è quella di un token del job.
func isJobIdentity(id trust.Identity) bool { return id.Username == trust.JobUsername }

// jobRouteAllowed: le sole rotte raggiungibili con l'identità del job.
//
//	POST /repos/{owner}/{repo}/releases
//	POST /repos/{owner}/{repo}/releases/{releaseId}/assets
//	POST /repos/{owner}/{repo}/apps/{app}/deploys (GIT-349, H7)
func jobRouteAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) == 6 && p[0] == "repos" && p[1] != "" && p[2] != "" && p[3] == "apps" && p[4] != "" && p[5] == "deploys" {
		return true
	}
	if len(p) < 4 || p[0] != "repos" || p[1] == "" || p[2] == "" || p[3] != "releases" {
		return false
	}
	switch len(p) {
	case 4:
		return true
	case 6:
		return p[4] != "" && p[5] == "assets"
	}
	return false
}

// jobGuard è la seconda barriera: l'identità del job passa solo se firmata dal
// gateway, con UUID nullo, scope esatto e su una rotta di jobRouteAllowed.
func jobGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := trust.FromContext(r.Context())
		if ok && isJobIdentity(id) {
			if trust.CallerFromContext(r.Context()) != trust.CallerGateway || id.UserID != uuid.Nil.String() ||
				len(id.Scopes) != 1 || id.Scopes[0] != trust.JobScope || !jobRouteAllowed(r) {
				writeError(w, http.StatusForbidden, "forbidden", "Il token del job non vale per questa operazione.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// jobReleaseAccess verifica il token del job per le release di owner/name e
// ritorna l'accesso (write) del job al suo repo. Ha già risposto se ok è false.
func (s *apiServer) jobReleaseAccess(w http.ResponseWriter, r *http.Request, owner, name string) (issueAccess, bool) {
	token := strings.TrimSpace(r.Header.Get(trust.HeaderJobToken))
	if token == "" || s.pool == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Token del job assente o non valido.")
		return issueAccess{}, false
	}
	a, err := ci.ResolveJobToken(r.Context(), s.pool, s.now(), token)
	if errors.Is(err, ci.ErrJobToken) {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Token del job assente o non valido.")
		return issueAccess{}, false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Impossibile verificare il token del job.")
		return issueAccess{}, false
	}
	// Il repo è quello del job e nessun altro: un altro repo, anche esistente, è 404
	// (come per il registry), così il token non rivela nulla degli altri repo.
	repo, err := s.resources.GetRepoByName(r.Context(), owner, name)
	if err != nil || repo.ID != a.RepoID {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura del repo.")
			return issueAccess{}, false
		}
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		return issueAccess{}, false
	}
	var raw []byte
	if err := s.pool.QueryRow(r.Context(), `SELECT permissions FROM core.ci_jobs WHERE id = $1`, a.JobID).Scan(&raw); err != nil {
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Impossibile leggere i permessi del job.")
		return issueAccess{}, false
	}
	var perms map[string]string
	_ = json.Unmarshal(raw, &perms)
	if perms["contents"] != ci.LevelWrite {
		writeError(w, http.StatusForbidden, "forbidden", "Il token del job non ha contents: write.")
		return issueAccess{}, false
	}
	return issueAccess{repo: repo, userID: uuid.Nil, caller: ciSystemCaller, write: true}, true
}
