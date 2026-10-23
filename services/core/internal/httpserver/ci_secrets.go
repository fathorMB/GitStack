package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// Segreti e variabili della CI e permessi di default del token del job (GIT-279, CI4).
// Li gestisce chi ha `admin` sul repo e gli owner dell'organizzazione (stesse regole dei
// webhook). Il valore di un segreto non torna da nessuna risposta.

var ciNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func ciTarget(sc hookScope) ci.Target {
	if sc.repo != nil {
		return ci.Target{RepoID: sc.repo.ID}
	}
	return ci.Target{Org: true, OrgID: sc.org.ID}
}

func (s *apiServer) ciFailure(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, ci.ErrTooMany):
		writeError(w, http.StatusConflict, "too_many", "Troppi segreti o variabili per questo bersaglio.")
	case errors.Is(err, webhooks.ErrNoKey):
		writeError(w, http.StatusServiceUnavailable, "ci_secrets_unavailable", "I segreti della CI non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
	default:
		slog.Default().Error(what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Errore interno.")
	}
}

func ciNameOK(w http.ResponseWriter, name string) bool {
	if !ciNameRe.MatchString(name) || len(name) > ci.MaxNameLen || strings.HasPrefix(name, "GITHUB_") || strings.HasPrefix(name, "GITSTACK_") {
		writeFieldError(w, "name", "name: maiuscole, cifre e _, senza iniziare per cifra o per GITHUB_/GITSTACK_ (al massimo 100 caratteri).")
		return false
	}
	return true
}

func (s *apiServer) ciKeys() ci.Sealer { return s.hooks.Keys }

func (s *apiServer) ciList(w http.ResponseWriter, r *http.Request, sc hookScope, secret bool) {
	rows, err := ci.List(r.Context(), s.pool, ciTarget(sc), secret)
	if err != nil {
		s.ciFailure(w, "elenco segreti/variabili CI", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if secret {
		out := openapi.ActionsSecretList{Items: []openapi.ActionsSecret{}}
		for _, e := range rows {
			out.Items = append(out.Items, openapi.ActionsSecret{Name: e.Name, UpdatedAt: e.UpdatedAt})
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	out := openapi.ActionsVariableList{Items: []openapi.ActionsVariable{}}
	for _, e := range rows {
		out.Items = append(out.Items, openapi.ActionsVariable{Name: e.Name, Value: e.Value, UpdatedAt: e.UpdatedAt})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *apiServer) ciPutSecret(w http.ResponseWriter, r *http.Request, sc hookScope) {
	var in openapi.ActionsSecretInput
	if !decodeIssueJSON(w, r, &in, false) || !ciNameOK(w, in.Name) {
		return
	}
	if in.Value == nil || *in.Value == "" || len(*in.Value) > ci.MaxSecretBytes {
		writeFieldError(w, "value", "value: obbligatorio, al massimo 48 KiB.")
		return
	}
	e, err := ci.PutSecret(r.Context(), s.pool, s.ciKeys(), s.now(), ciTarget(sc), sc.userID, in.Name, *in.Value)
	if err != nil {
		s.ciFailure(w, "salvataggio del segreto CI", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openapi.ActionsSecret{Name: e.Name, UpdatedAt: e.UpdatedAt})
}

func (s *apiServer) ciPutVariable(w http.ResponseWriter, r *http.Request, sc hookScope) {
	var in openapi.ActionsVariableInput
	if !decodeIssueJSON(w, r, &in, false) || !ciNameOK(w, in.Name) {
		return
	}
	if len(in.Value) > ci.MaxVariableBytes {
		writeFieldError(w, "value", "value: al massimo 64 KiB.")
		return
	}
	e, err := ci.PutVariable(r.Context(), s.pool, s.now(), ciTarget(sc), sc.userID, in.Name, in.Value)
	if err != nil {
		s.ciFailure(w, "salvataggio della variabile CI", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openapi.ActionsVariable{Name: e.Name, Value: e.Value, UpdatedAt: e.UpdatedAt})
}

func (s *apiServer) ciDelete(w http.ResponseWriter, r *http.Request, sc hookScope, secret bool, name string) {
	if !ciNameOK(w, name) {
		return
	}
	ok, err := ci.Delete(r.Context(), s.pool, ciTarget(sc), secret, name)
	if err != nil {
		s.ciFailure(w, "eliminazione CI", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- repo ----

func (s *apiServer) ListRepositoryActionsSecrets(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, false); ok {
		s.ciList(w, r, sc, true)
	}
}

func (s *apiServer) PutRepositoryActionsSecret(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, true); ok {
		s.ciPutSecret(w, r, sc)
	}
}

func (s *apiServer) DeleteRepositoryActionsSecret(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, name openapi.ActionsNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, true); ok {
		s.ciDelete(w, r, sc, true, name)
	}
}

func (s *apiServer) ListRepositoryActionsVariables(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, false); ok {
		s.ciList(w, r, sc, false)
	}
}

func (s *apiServer) PutRepositoryActionsVariable(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, true); ok {
		s.ciPutVariable(w, r, sc)
	}
}

func (s *apiServer) DeleteRepositoryActionsVariable(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, name openapi.ActionsNameParam) {
	if sc, ok := s.hookRepoScope(w, r, o, n, true); ok {
		s.ciDelete(w, r, sc, false, name)
	}
}

// ---- organizzazione ----

func (s *apiServer) ListOrganizationActionsSecrets(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciList(w, r, sc, true)
	}
}

func (s *apiServer) PutOrganizationActionsSecret(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciPutSecret(w, r, sc)
	}
}

func (s *apiServer) DeleteOrganizationActionsSecret(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, name openapi.ActionsNameParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciDelete(w, r, sc, true, name)
	}
}

func (s *apiServer) ListOrganizationActionsVariables(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciList(w, r, sc, false)
	}
}

func (s *apiServer) PutOrganizationActionsVariable(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciPutVariable(w, r, sc)
	}
}

func (s *apiServer) DeleteOrganizationActionsVariable(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, name openapi.ActionsNameParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.ciDelete(w, r, sc, false, name)
	}
}

// ---- permessi di default del token del job ----

func tokenDefaultsView(d ci.RepoTokenDefaults) openapi.ActionsJobTokenDefaults {
	return openapi.ActionsJobTokenDefaults{
		Contents: openapi.ActionsJobTokenDefaultsContents(d.Contents),
		Packages: openapi.ActionsJobTokenDefaultsPackages(d.Packages),
		Actions:  openapi.ActionsJobTokenDefaultsActions(d.Actions),
	}
}

func (s *apiServer) GetRepositoryActionsPermissions(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, o, n, false)
	if !ok {
		return
	}
	d, err := ci.LoadRepoTokenDefaults(r.Context(), s.pool, sc.repo.ID)
	if err != nil {
		s.ciFailure(w, "lettura dei permessi del token del job", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenDefaultsView(d))
}

func (s *apiServer) PutRepositoryActionsPermissions(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, o, n, true)
	if !ok {
		return
	}
	var in openapi.ActionsJobTokenDefaults
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	d := ci.RepoTokenDefaults{Contents: string(in.Contents), Packages: string(in.Packages), Actions: string(in.Actions)}
	if !d.Valid() {
		writeFieldError(w, "contents", "contents, packages e actions: none, read o write.")
		return
	}
	if err := ci.SaveRepoTokenDefaults(r.Context(), s.pool, sc.repo.ID, d); err != nil {
		s.ciFailure(w, "salvataggio dei permessi del token del job", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenDefaultsView(d))
}
