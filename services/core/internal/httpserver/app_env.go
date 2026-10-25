package httpserver

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// Variabili e segreti delle app (v3-a, GIT-348, H8). Permessi: le variabili si leggono con `write`
// (read riceve 403); creare, cambiare ed eliminare richiede `admin`; i segreti si elencano solo con
// `admin` e il loro valore non torna mai da nessuna risposta. Ogni modifica crea una versione
// (trigger config). I segreti delle app non sono quelli della CI: tabelle e API separate.

const maxAppEnvValueBytes = 32 << 10

var appEnvNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func appEnvNameOK(w http.ResponseWriter, name string) bool {
	if len(name) > 100 || !appEnvNameRe.MatchString(name) {
		writeFieldError(w, "name", "name: lettere, cifre e _, senza iniziare per cifra (al massimo 100 caratteri).")
		return false
	}
	return true
}

func appEnvVariableOut(v store.AppVariable) openapi.AppVariable {
	ver := v.Version
	// managed (variabili impostate da GitStack, H6) non esiste ancora: tutte quelle dell'utente sono false.
	return openapi.AppVariable{Name: v.Name, Value: v.Value, Managed: false, Version: &ver, UpdatedAt: v.UpdatedAt, UpdatedBy: actorToAPI(v.UpdatedBy)}
}

func appEnvSecretOut(m store.AppSecretMeta) openapi.AppSecret {
	ver := m.Version
	return openapi.AppSecret{Name: m.Name, Version: &ver, UpdatedAt: m.UpdatedAt, UpdatedBy: actorToAPI(m.UpdatedBy)}
}

// appEnvFailure traduce gli errori dello store: nome già in uso (409), segreti non configurati (503).
func appEnvFailure(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
	case errors.Is(err, store.ErrEnvNameInUse):
		writeError(w, http.StatusConflict, "name_in_use", "Il nome è già usato da una variabile o da un segreto dell'app.")
	case errors.Is(err, webhooks.ErrNoKey):
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
	default:
		appInternalError(w, what, err)
	}
}

// ListAppVariables implementa GET .../apps/{app}/variables: `write` le legge.
func (s *apiServer) ListAppVariables(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "write", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	vs, err := s.resources.ListAppVariables(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "elenco delle variabili dell'app", err)
		return
	}
	out := openapi.AppVariableList{Items: []openapi.AppVariable{}}
	for _, v := range vs {
		out.Items = append(out.Items, appEnvVariableOut(v))
	}
	writeJSON(w, http.StatusOK, out)
}

// PutAppVariable implementa PUT .../apps/{app}/variables/{name}: crea o sostituisce, `admin`.
func (s *apiServer) PutAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppVariableInput
	if !decodeIssueJSON(w, r, &in, false) || !appEnvNameOK(w, name) {
		return
	}
	if len(in.Value) > maxAppEnvValueBytes {
		writeFieldError(w, "value", "value: al massimo 32 KiB.")
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	v, _, err := s.resources.PutAppVariable(r.Context(), a.ID, name, in.Value, actor.store(), s.now())
	if err != nil {
		appEnvFailure(w, "salvataggio della variabile dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, appEnvVariableOut(v))
}

// DeleteAppVariable implementa DELETE .../apps/{app}/variables/{name}: `admin`, 404 se il nome non c'è.
func (s *apiServer) DeleteAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	if _, err := s.resources.DeleteAppVariable(r.Context(), a.ID, name, actor.store(), s.now()); err != nil {
		appEnvFailure(w, "eliminazione della variabile dell'app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAppSecrets implementa GET .../apps/{app}/secrets: solo metadati, `admin`.
func (s *apiServer) ListAppSecrets(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "admin", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	ms, err := s.resources.ListAppSecrets(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "elenco dei segreti dell'app", err)
		return
	}
	out := openapi.AppSecretList{Items: []openapi.AppSecret{}}
	for _, m := range ms {
		out.Items = append(out.Items, appEnvSecretOut(m))
	}
	writeJSON(w, http.StatusOK, out)
}

// PutAppSecret implementa PUT .../apps/{app}/secrets/{name}: cifra il valore, `admin`. Risponde
// solo con i metadati: il valore non torna mai.
func (s *apiServer) PutAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppSecretInput
	if !decodeIssueJSON(w, r, &in, false) || !appEnvNameOK(w, name) {
		return
	}
	if in.Value == nil || *in.Value == "" || len(*in.Value) > maxAppEnvValueBytes {
		writeFieldError(w, "value", "value: obbligatorio, al massimo 32 KiB.")
		return
	}
	if !s.hooks.Keys.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	m, _, err := s.resources.PutAppSecret(r.Context(), s.hooks.Keys, a.ID, name, *in.Value, actor.store(), s.now())
	if err != nil {
		appEnvFailure(w, "salvataggio del segreto dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, appEnvSecretOut(m))
}

// DeleteAppSecret implementa DELETE .../apps/{app}/secrets/{name}: `admin`, 404 se il nome non c'è.
func (s *apiServer) DeleteAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	if _, err := s.resources.DeleteAppSecret(r.Context(), a.ID, name, actor.store(), s.now()); err != nil {
		appEnvFailure(w, "eliminazione del segreto dell'app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
