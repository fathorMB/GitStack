package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"
)

// GetUserAccess implementa GET /users/{username}/access: i repo raggiungibili
// da un utente con ruolo effettivo e provenienza (P1-P6). Solo
// l'amministratore dell'installazione. Identity conosce grant, team e
// attributi ma non i nomi dei repo, core i nomi ma non i permessi: core
// chiede a identity le fonti e le unisce ai suoi repo non eliminati.
//
// Ordine: pagina (400) → identità (401) → admin (403) → utente (404).
func (s *apiServer) GetUserAccess(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam, params openapi.GetUserAccessParams) {
	page, perPage := defaultPage, defaultPerPage
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return
	}
	_, callerID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.readable == nil || s.repoIdentity == nil || s.userAccess == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile calcolare l'accesso.")
		return
	}
	unavailable := func(err error, what string) {
		slog.Default().Warn(what, "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile calcolare l'accesso.")
	}
	// all è vero solo per l'amministratore dell'installazione.
	callerIsAdmin, _, err := s.readable.ReadableResources(r.Context(), callerID)
	if err != nil {
		unavailable(err, "verifica dell'amministratore non riuscita")
		return
	}
	if !callerIsAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'amministratore dell'installazione può vedere l'accesso di un utente.")
		return
	}
	owner, err := s.repoIdentity.ResolveOwner(r.Context(), string(username))
	if errors.Is(err, identityclient.ErrNotFound) || (err == nil && owner.Type != "user") {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		unavailable(err, "risoluzione dell'utente non riuscita")
		return
	}
	access, err := s.userAccess.UserAccess(r.Context(), owner.ID)
	if errors.Is(err, identityclient.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		unavailable(err, "lettura dell'accesso dell'utente non riuscita")
		return
	}

	byID := make(map[uuid.UUID]identityclient.ResourceAccess, len(access.Items))
	var visible []uuid.UUID // nil = tutti i repo (amministratore dell'installazione)
	if !access.Admin {
		visible = make([]uuid.UUID, 0, len(access.Items))
		for _, it := range access.Items {
			byID[it.ResourceID] = it
			visible = append(visible, it.ResourceID)
		}
	}
	repos, total, err := s.resources.ListRepos(r.Context(), visible, nil, page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura dei repo.")
		return
	}
	out := make([]openapi.UserAccessItem, 0, len(repos))
	for _, repo := range repos {
		item := openapi.UserAccessItem{
			RepositoryId: openapi_types.UUID(repo.ID),
			Owner:        openapi.RepoOwner{Type: openapi.OwnerType(repo.OwnerType), Name: repo.OwnerName},
			Name:         repo.Name,
			FullName:     repo.OwnerName + "/" + repo.Name,
		}
		vis := openapi.RepoVisibility(repo.Visibility)
		item.Visibility = &vis
		if access.Admin {
			item.Role = openapi.Admin
			item.Sources = []openapi.AccessSource{{Kind: openapi.AccessSourceKindInstallationAdmin, Role: openapi.Admin}}
		} else {
			ra := byID[repo.ID]
			item.Role = openapi.ResourceRole(ra.Role)
			item.Sources = make([]openapi.AccessSource, 0, len(ra.Sources))
			for _, src := range ra.Sources {
				a := openapi.AccessSource{Kind: openapi.AccessSourceKind(src.Kind), Role: openapi.ResourceRole(src.Role)}
				if src.Organization != "" {
					org := src.Organization
					a.Organization = &org
				}
				if src.Team != "" {
					tm := src.Team
					a.Team = &tm
				}
				item.Sources = append(item.Sources, a)
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, openapi.UserAccessList{Items: out, Page: page, PerPage: perPage, Total: total})
}
