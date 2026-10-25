package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func quotaWarning(used, limits store.AppQuotaAmounts) bool {
	return float64(used.CPUMillis) >= .8*float64(limits.CPUMillis) ||
		float64(used.MemoryBytes) >= .8*float64(limits.MemoryBytes) ||
		float64(used.DiskBytes) >= .8*float64(limits.DiskBytes)
}

func quotaToAPI(q store.AppOwnerQuota) openapi.AppOwnerQuota {
	return openapi.AppOwnerQuota{Owner: q.Owner, IsDefault: &q.IsDefault, Warning: quotaWarning(q.Used, q.Limits),
		Limits: openapi.AppOwnerQuotaInput{CpuMillis: q.Limits.CPUMillis, MemoryBytes: q.Limits.MemoryBytes, DiskBytes: q.Limits.DiskBytes},
		Used:   openapi.AppOwnerQuotaInput{CpuMillis: q.Used.CPUMillis, MemoryBytes: q.Used.MemoryBytes, DiskBytes: q.Used.DiskBytes}}
}

func capacityToAPI(c store.AppCapacity, ciSlots int) openapi.AppsCapacity {
	if ciSlots < 1 {
		ciSlots = 3
	}
	var out openapi.AppsCapacity
	out.Server.CpuMillis, out.Server.MemoryBytes = c.Server.CPUMillis, c.Server.MemoryBytes
	out.Services.Reserved = openapi.AppResourceAmount{CpuMillis: c.Services.CPUMillis, MemoryBytes: c.Services.MemoryBytes}
	out.Apps.Share = openapi.AppResourceAmount{CpuMillis: c.Share.CPUMillis, MemoryBytes: c.Share.MemoryBytes}
	out.Apps.Guaranteed = openapi.AppResourceAmount{CpuMillis: c.Used.CPUMillis, MemoryBytes: c.Used.MemoryBytes}
	out.Apps.ShareIsDefault = &c.Default
	out.Ci.Slots, out.Ci.GuaranteedCpuMillis = ciSlots, ciSlots*2000
	out.AppsShareWarning = float64(c.Used.CPUMillis) >= .8*float64(c.Share.CPUMillis) || float64(c.Used.MemoryBytes) >= .8*float64(c.Share.MemoryBytes)
	return out
}

// GetAdminAppsCapacity implementa GET /admin/apps/capacity.
func (s *apiServer) GetAdminAppsCapacity(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere la capacita' delle app"); !ok {
		return
	}
	c, err := s.resources.AppCapacity(r.Context())
	if err != nil {
		appInternalError(w, "lettura della capacita' delle app", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityToAPI(c, s.ciCapacity))
}

// UpdateAdminAppsReserve implementa PUT /admin/apps/reserve.
func (s *apiServer) UpdateAdminAppsReserve(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "modificare la riserva delle app"); !ok {
		return
	}
	var in openapi.AppsReserveInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.CpuMillis < 1 || in.MemoryBytes < 1 {
		writeFieldError(w, "resources", "CPU e memoria devono essere positive.")
		return
	}
	c, err := s.resources.SetAppCapacity(r.Context(), store.AppResourceAmount{CPUMillis: in.CpuMillis, MemoryBytes: in.MemoryBytes})
	var reserve *store.ReserveCapacityError
	if errors.As(err, &reserve) {
		writeErrorDetails(w, http.StatusUnprocessableEntity, "reserve_below_guaranteed", "La riserva non basta per le parti garantite o per i servizi GitStack.", map[string]any{"missing": reserve.Missing})
		return
	}
	if err != nil {
		appInternalError(w, "aggiornamento della riserva delle app", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityToAPI(c, s.ciCapacity))
}

// ListAdminAppOwnerQuotas implementa GET /admin/apps/quotas.
func (s *apiServer) ListAdminAppOwnerQuotas(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le quote delle app"); !ok {
		return
	}
	items, err := s.resources.ListAppOwnerQuotas(r.Context())
	if err != nil {
		appInternalError(w, "elenco delle quote delle app", err)
		return
	}
	out := openapi.AppOwnerQuotaList{Items: make([]openapi.AppOwnerQuota, 0, len(items))}
	for _, q := range items {
		out.Items = append(out.Items, quotaToAPI(q))
	}
	writeJSON(w, http.StatusOK, out)
}

// PutAdminAppOwnerQuota implementa PUT /admin/apps/quotas/{owner}.
func (s *apiServer) PutAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	if _, ok := s.requireInstanceAdmin(w, r, "modificare le quote delle app"); !ok {
		return
	}
	var in openapi.AppOwnerQuotaInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.CpuMillis < 0 || in.MemoryBytes < 0 || in.DiskBytes < 0 {
		writeFieldError(w, "quota", "I limiti della quota non possono essere negativi.")
		return
	}
	name := strings.ToLower(string(owner))
	used, err := s.resources.ValidateOwnerQuota(r.Context(), name, store.AppQuotaAmounts{CPUMillis: in.CpuMillis, MemoryBytes: in.MemoryBytes, DiskBytes: in.DiskBytes})
	var ce *store.CapacityError
	if errors.As(err, &ce) {
		writeErrorDetails(w, http.StatusUnprocessableEntity, "quota_below_usage", "La quota non puo' essere sotto l'uso gia' garantito.", map[string]any{"used": used})
		return
	}
	if err != nil {
		appInternalError(w, "verifica della quota delle app", err)
		return
	}
	if err := s.resources.SetOwnerAppQuota(r.Context(), name, store.AppQuotaAmounts{CPUMillis: in.CpuMillis, MemoryBytes: in.MemoryBytes, DiskBytes: in.DiskBytes}); err != nil {
		appInternalError(w, "salvataggio della quota delle app", err)
		return
	}
	writeJSON(w, http.StatusOK, quotaToAPI(store.AppOwnerQuota{Owner: name, Limits: store.AppQuotaAmounts{CPUMillis: in.CpuMillis, MemoryBytes: in.MemoryBytes, DiskBytes: in.DiskBytes}, Used: used}))
}

// DeleteAdminAppOwnerQuota implementa DELETE /admin/apps/quotas/{owner}.
func (s *apiServer) DeleteAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	if _, ok := s.requireInstanceAdmin(w, r, "eliminare le quote delle app"); !ok {
		return
	}
	err := s.resources.DeleteOwnerAppQuota(r.Context(), string(owner))
	if errors.Is(err, store.ErrAppOwnerNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Quota dell'owner non trovata.")
		return
	}
	if err != nil {
		appInternalError(w, "eliminazione della quota delle app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
