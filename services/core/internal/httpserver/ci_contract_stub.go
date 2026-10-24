package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Le superfici CI, registry e release sono fissate dal contratto GIT-275;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.
// Artefatti, cache, coda e impostazioni di archiviazione sono in ci_storage.go (GIT-280).
func notImplemented(w http.ResponseWriter) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *apiServer) ListCiRunners(w http.ResponseWriter, r *http.Request) { notImplemented(w) }
func (s *apiServer) CreateCiRunnerRegistrationToken(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}
func (s *apiServer) DeleteCiRunner(w http.ResponseWriter, r *http.Request, id openapi.RunnerIdParam) {
	notImplemented(w)
}
func (s *apiServer) ListRepositoryPackages(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	notImplemented(w)
}
