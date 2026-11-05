package httpserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// adminAppRow converte un'app e il suo uso corrente nella riga dell'elenco admin (H9, H11).
func adminAppRow(a store.App, u store.AppUsageNow, signedOnly bool) openapi.AdminApp {
	status, _ := a.Effective()
	copies := a.Config.Copies
	above := copies > 0 && (u.CPUMillis > a.Config.Guaranteed.CPUMillis*copies || u.MemoryBytes > a.Config.Guaranteed.MemoryBytes*int64(copies))
	row := openapi.AdminApp{
		Owner: a.Owner, Repo: a.RepoName, Name: openapi.AppName(a.Name), Status: openapi.AppStatus(status),
		Copies:          copies,
		Guaranteed:      openapi.AppResourceAmount{CpuMillis: a.Config.Guaranteed.CPUMillis, MemoryBytes: a.Config.Guaranteed.MemoryBytes},
		Limit:           openapi.AppResourceAmount{CpuMillis: a.Config.Limit.CPUMillis, MemoryBytes: a.Config.Limit.MemoryBytes},
		Usage:           openapi.AppUsage{CpuMillis: u.CPUMillis, MemoryBytes: u.MemoryBytes},
		AboveGuaranteed: above, Verified: a.Image.Verified, RunsAsRoot: a.Image.RunsAsRoot,
	}
	violates := signedOnly && !a.Image.Verified
	row.ViolatesInstanceRules = &violates
	return row
}

// ListAdminApps implementa GET /admin/apps: le app di tutta l'istanza, solo per l'amministratore.
func (s *apiServer) ListAdminApps(w http.ResponseWriter, r *http.Request, params openapi.ListAdminAppsParams) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le app dell'istanza"); !ok {
		return
	}
	page, perPage, ok := pageArgs(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	apps, total, err := s.resources.ListInstanceApps(r.Context(), store.AppListFilter{}, perPage, (page-1)*perPage)
	if err != nil {
		appInternalError(w, "elenco delle app dell'istanza", err)
		return
	}
	ids := make([]uuid.UUID, 0, len(apps))
	for _, a := range apps {
		ids = append(ids, a.ID)
	}
	usage, err := s.resources.AppsUsageNow(r.Context(), ids, time.Now().UTC().Add(-appCopyFreshness))
	if err != nil {
		appInternalError(w, "lettura dell'uso delle app", err)
		return
	}
	signedOnly, err := s.resources.AppSignedImagesOnly(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle impostazioni delle app", err)
		return
	}
	out := openapi.AdminAppList{Items: make([]openapi.AdminApp, 0, len(apps)), Total: total}
	for _, a := range apps {
		out.Items = append(out.Items, adminAppRow(a, usage[a.ID], signedOnly))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetAppResources implementa GET /repos/{owner}/{repo}/apps/{app}/resources (serve `read`).
func (s *apiServer) GetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	s.writeAppResources(w, r, a)
}

func (s *apiServer) writeAppResources(w http.ResponseWriter, r *http.Request, a store.App) {
	c, err := s.resources.AppCapacity(r.Context())
	if err != nil {
		appInternalError(w, "lettura della capacita' delle app", err)
		return
	}
	out := resourcesToAPI(a.Config)
	out.MaxPerApp = &openapi.AppResourceAmount{CpuMillis: c.Max.CPUMillis, MemoryBytes: c.Max.MemoryBytes}
	writeJSON(w, http.StatusOK, out)
}

// SetAppResources implementa PUT /repos/{owner}/{repo}/apps/{app}/resources (serve `admin`).
func (s *apiServer) SetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	if _, ok := s.appFor(w, r, repo, app); !ok {
		return
	}
	var in openapi.AppResourcesInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if m := validAmounts(in.Guaranteed, in.Limit); m != "" {
		writeFieldError(w, "resources", m)
		return
	}
	updated, _, err := s.resources.SetAppResources(r.Context(), repo.ID, app,
		store.AppResourceAmount{CPUMillis: in.Guaranteed.CpuMillis, MemoryBytes: in.Guaranteed.MemoryBytes},
		store.AppResourceAmount{CPUMillis: in.Limit.CpuMillis, MemoryBytes: in.Limit.MemoryBytes}, actor.store(), s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		return
	case errors.Is(err, store.ErrAppLimitTooHigh):
		writeErrorDetails(w, http.StatusUnprocessableEntity, "limit_too_high", "Il tetto supera il massimo per app dell'istanza.", nil)
		return
	case err != nil:
		if !s.writeAppConflict(w, err) {
			appInternalError(w, "modifica delle risorse dell'app", err)
		}
		return
	}
	s.writeAppResources(w, r, updated)
}

// ListUserApps implementa GET /user/apps: le app dei repo che il chiamante può leggere (stesso
// criterio di GET /repos: readable-resources di identity), paginate. Con l'hosting spento di
// proposito risponde 200 con elenco vuoto e hostingEnabled=false, non 503 (GIT-548).
func (s *apiServer) ListUserApps(w http.ResponseWriter, r *http.Request, params openapi.ListUserAppsParams) {
	page, perPage, ok := pageArgs(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.appsOff {
		writeJSON(w, http.StatusOK, openapi.UserAppList{Items: []openapi.App{}, Page: page, PerPage: perPage, HostingEnabled: false})
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile filtrare le app per permesso.")
		return
	}
	all, ids, err := s.repoIdentity.ReadableResources(r.Context(), userID)
	if err != nil {
		appInternalError(w, "elenco delle risorse leggibili", err)
		return
	}
	filter := store.AppListFilter{}
	if !all {
		filter.RepoIDs = ids
		if filter.RepoIDs == nil {
			filter.RepoIDs = []uuid.UUID{}
		}
	}
	apps, total, err := s.resources.ListInstanceApps(r.Context(), filter, perPage, (page-1)*perPage)
	if err != nil {
		appInternalError(w, "elenco delle app dell'utente", err)
		return
	}
	publicOn, err := s.resources.PublicAppsEnabled(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle app pubbliche", err)
		return
	}
	byApp := map[uuid.UUID][]store.AppDomain{}
	if publicOn {
		domains, err := s.resources.ListAppDomainsForCheck(r.Context())
		if err != nil {
			appInternalError(w, "lettura dei domini", err)
			return
		}
		for _, d := range domains {
			byApp[d.AppID] = append(byApp[d.AppID], d)
		}
	}
	out := openapi.UserAppList{Items: make([]openapi.App, 0, len(apps)), Page: page, PerPage: perPage, Total: total, HostingEnabled: true}
	for _, a := range apps {
		out.Items = append(out.Items, s.appToAPIWith(a, publicOn, byApp[a.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}
