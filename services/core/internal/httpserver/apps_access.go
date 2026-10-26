package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Modo di accesso delle app (v3-a, GIT-347, H6). `login` è il default: davanti all'app c'è il
// login GitStack (forward-auth del gateway) e passa chi ha `read` sul repo. `none` (senza
// login) è una scelta esplicita di chi ha `admin` sul repo: solo SetAppAccess lo imposta e la
// creazione dell'app parte sempre da `login`. `public` richiede le app pubbliche abilitate
// dall'amministratore (H1, doppio consenso): l'impostazione non esiste ancora, quindi risponde
// sempre 422 `public_apps_disabled`.

// appAccessToAPI traduce lo stato dell'app nella risposta di GET/PUT .../access. Il contratto ha
// changedAt/changedBy ma il modello non li conserva (nessuna migrazione in questo item): restano assenti.
func (s *apiServer) appAccessToAPI(a store.App) openapi.AppAccess {
	mode := openapi.AppAccessMode(a.Access)
	out := openapi.AppAccess{Mode: mode, PublicAppsEnabled: false, Address: s.appAddress(a.AddressLabel)}
	if out.Address != nil && mode != openapi.AppAccessModePublic {
		c := openapi.InstanceCa
		out.Certificate = &c
	}
	headers := mode == openapi.AppAccessModeLogin
	out.IdentityHeaders = &headers
	return out
}

// GetAppAccess implementa GET /repos/{owner}/{repo}/apps/{app}/access.
func (s *apiServer) GetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.appAccessToAPI(a))
}

// SetAppAccess implementa PUT /repos/{owner}/{repo}/apps/{app}/access (admin sul repo).
func (s *apiServer) SetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppAccessInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIssueRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	switch in.Mode {
	case openapi.AppAccessModeLogin, openapi.AppAccessModeNone:
	case openapi.AppAccessModePublic:
		writeError(w, http.StatusUnprocessableEntity, "public_apps_disabled", "Le app pubbliche non sono abilitate dall'amministratore.")
		return
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "mode: login, none o public.")
		return
	}
	if _, ok := s.appFor(w, r, repo, app); !ok {
		return
	}
	updated, err := s.resources.SetAppAccess(r.Context(), repo.ID, app, string(in.Mode), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		return
	}
	if err != nil {
		appInternalError(w, "modifica dell'accesso dell'app", err)
		return
	}
	// La rotta e i suoi middleware dipendono dal modo: il controller li riallinea.
	if s.appCtl != nil {
		s.appCtl.Kick()
	}
	writeJSON(w, http.StatusOK, s.appAccessToAPI(updated))
}
