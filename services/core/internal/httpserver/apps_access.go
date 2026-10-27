package httpserver

import (
	"context"
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
// dall'amministratore (H1, doppio consenso, GIT-456): con l'interruttore spento risponde 422
// `public_apps_disabled`. Spegnere l'interruttore non cancella il modo scelto: l'app non è esposta
// (lo decide il controller) e la riaccensione la ripristina. Ogni cambio va nello storico con l'attore.

// appAccessToAPI traduce lo stato dell'app nella risposta di GET/PUT .../access. Il contratto ha
// changedAt/changedBy vengono dall'ultimo evento dello storico dell'app (assenti se non ce ne sono).
func (s *apiServer) appAccessToAPI(ctx context.Context, a store.App) (openapi.AppAccess, error) {
	mode := openapi.AppAccessMode(a.Access)
	publicOn, err := s.resources.PublicAppsEnabled(ctx)
	if err != nil {
		return openapi.AppAccess{}, err
	}
	out := openapi.AppAccess{Mode: mode, PublicAppsEnabled: publicOn, Address: s.appAddress(a.AddressLabel)}
	if evs, err := s.resources.ListAppAccessEvents(ctx, &a.ID, false, 1); err != nil {
		return openapi.AppAccess{}, err
	} else if len(evs) > 0 {
		at := evs[0].CreatedAt
		out.ChangedAt = &at
		out.ChangedBy = actorToAPI(evs[0].Actor)
	}
	if out.Address != nil && mode != openapi.AppAccessModePublic {
		c := openapi.AppAccessCertificateInstanceCa
		out.Certificate = &c
	}
	headers := mode == openapi.AppAccessModeLogin
	out.IdentityHeaders = &headers
	return out, nil
}

// writeAppAccess risponde con lo stato di accesso dell'app.
func (s *apiServer) writeAppAccess(w http.ResponseWriter, r *http.Request, a store.App) {
	out, err := s.appAccessToAPI(r.Context(), a)
	if err != nil {
		appInternalError(w, "lettura dell'accesso dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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
	s.writeAppAccess(w, r, a)
}

// SetAppAccess implementa PUT /repos/{owner}/{repo}/apps/{app}/access (admin sul repo).
func (s *apiServer) SetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
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
		on, err := s.resources.PublicAppsEnabled(r.Context())
		if err != nil {
			appInternalError(w, "lettura delle app pubbliche", err)
			return
		}
		if on {
			break
		}
		writeError(w, http.StatusUnprocessableEntity, "public_apps_disabled", "Le app pubbliche non sono abilitate dall'amministratore.")
		return
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "mode: login, none o public.")
		return
	}
	if _, ok := s.appFor(w, r, repo, app); !ok {
		return
	}
	updated, err := s.resources.SetAppAccessAudited(r.Context(), repo.ID, app, string(in.Mode), actor.store(), s.now())
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
	s.writeAppAccess(w, r, updated)
}
