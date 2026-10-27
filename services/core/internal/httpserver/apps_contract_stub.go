package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func appStringPtr(v string) *string { return &v }
func appBoolPtr(v bool) *bool       { return &v }

// Le operazioni delle app (v3-a, H1-H13) sono fissate dal contratto GIT-341;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.
func notImplemented(w http.ResponseWriter) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *apiServer) GetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) SetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) ListAdminApps(w http.ResponseWriter, r *http.Request, params openapi.ListAdminAppsParams) {
	notImplemented(w)
}

func (s *apiServer) GetAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le impostazioni delle app"); !ok {
		return
	}
	c, err := s.resources.AppCapacity(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle impostazioni delle app", err)
		return
	}
	signedOnly, err := s.resources.AppSignedImagesOnly(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle impostazioni delle app", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.AppsInstanceSettings{AppsDomain: appStringPtr(s.appsDomain), AppsDomainIsDefault: appBoolPtr(s.appsDomain == ""), MaxPerApp: openapi.AppResourceAmount{CpuMillis: c.Max.CPUMillis, MemoryBytes: c.Max.MemoryBytes}, PublicAppsEnabled: false, SignedImagesOnly: signedOnly, Restricted: false, LogRetention: openapi.AppLogRetention{Days: 7, MaxBytes: 1 << 30}})
}

func (s *apiServer) UpdateAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "modificare le impostazioni delle app"); !ok {
		return
	}
	var in openapi.AppsInstanceSettingsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.MaxPerApp.CpuMillis < 1 || in.MaxPerApp.MemoryBytes < 1 {
		writeFieldError(w, "maxPerApp", "CPU e memoria devono essere positive.")
		return
	}
	if in.PublicAppsEnabled {
		writeFieldError(w, "publicAppsEnabled", "non ancora configurabile")
		return
	}
	if in.Restricted {
		writeFieldError(w, "restricted", "non ancora configurabile")
		return
	}
	if in.LogRetention.Days != 7 || in.LogRetention.MaxBytes != 1<<30 {
		writeFieldError(w, "logRetention", "non ancora configurabile")
		return
	}
	c, err := s.resources.SetAppMax(r.Context(), store.AppResourceAmount{CPUMillis: in.MaxPerApp.CpuMillis, MemoryBytes: in.MaxPerApp.MemoryBytes})
	if err != nil {
		appInternalError(w, "aggiornamento del massimo per app", err)
		return
	}
	// «Solo immagini firmate» (H10): vale dai deploy successivi, le app in esecuzione non si fermano.
	if err := s.resources.SetAppSignedImagesOnly(r.Context(), in.SignedImagesOnly); err != nil {
		appInternalError(w, "aggiornamento di «solo immagini firmate»", err)
		return
	}
	signedOnly := in.SignedImagesOnly
	writeJSON(w, http.StatusOK, openapi.AppsInstanceSettings{AppsDomain: appStringPtr(s.appsDomain), AppsDomainIsDefault: appBoolPtr(s.appsDomain == ""), MaxPerApp: openapi.AppResourceAmount{CpuMillis: c.Max.CPUMillis, MemoryBytes: c.Max.MemoryBytes}, PublicAppsEnabled: false, SignedImagesOnly: signedOnly, Restricted: false, LogRetention: openapi.AppLogRetention{Days: 7, MaxBytes: 1 << 30}})
}
