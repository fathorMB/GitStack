package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Runner esterni (GIT-278, CI2): registrazione con token monouso, elenco, revoca.
// Solo l'amministratore dell'installazione: lo scope da solo non basta.

// requireInstanceAdmin risponde 401/403/503 e ritorna false se il chiamante non è admin.
func (s *apiServer) requireInstanceAdmin(w http.ResponseWriter, r *http.Request, what string) (uuid.UUID, bool) {
	_, callerID, ok := callerIdentity(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if s.readable == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare l'amministratore.")
		return uuid.Nil, false
	}
	isAdmin, _, err := s.readable.ReadableResources(r.Context(), callerID)
	if err != nil {
		slog.Default().Warn("verifica dell'amministratore non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare l'amministratore.")
		return uuid.Nil, false
	}
	if !isAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'amministratore dell'installazione può "+what+".")
		return uuid.Nil, false
	}
	return callerID, true
}

// ListCiRunners implementa GET /admin/ci/runners.
func (s *apiServer) ListCiRunners(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere i runner"); !ok {
		return
	}
	rows, err := ci.ListRunners(r.Context(), s.pool, s.now())
	if err != nil {
		slog.Default().Error("elenco dei runner non riuscito", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Elenco dei runner non disponibile.")
		return
	}
	out := openapi.CiRunnerList{Items: make([]openapi.CiRunner, 0, len(rows))}
	for _, rn := range rows {
		out.Items = append(out.Items, openapi.CiRunner{Id: rn.ID, Name: rn.Name, Labels: rn.Labels, Status: openapi.CiRunnerStatus(rn.Status)})
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateCiRunnerRegistrationToken implementa POST /admin/ci/runners/registration-token:
// il token in chiaro è solo in questa risposta 201 (nel database c'è lo SHA-256).
func (s *apiServer) CreateCiRunnerRegistrationToken(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.requireInstanceAdmin(w, r, "creare token di registrazione")
	if !ok {
		return
	}
	token, exp, err := ci.IssueRegistrationToken(r.Context(), s.pool, s.now(), &adminID)
	if err != nil {
		slog.Default().Error("token di registrazione non creato", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Token di registrazione non creato.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, openapi.CiRunnerRegistrationToken{Token: token, ExpiresAt: exp})
}

// DeleteCiRunner implementa DELETE /admin/ci/runners/{runnerId}: revoca il runner.
func (s *apiServer) DeleteCiRunner(w http.ResponseWriter, r *http.Request, id openapi.RunnerIdParam) {
	if _, ok := s.requireInstanceAdmin(w, r, "revocare un runner"); !ok {
		return
	}
	err := ci.DeleteRunner(r.Context(), s.pool, s.now(), uuid.UUID(id), ciReporter{s: s})
	switch {
	case errors.Is(err, ci.ErrRunnerNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Runner non trovato.")
	case err != nil:
		slog.Default().Error("revoca del runner non riuscita", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Revoca del runner non riuscita.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
