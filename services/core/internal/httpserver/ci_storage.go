package httpserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Artefatti, cache, coda e impostazioni di archiviazione della CI (GIT-280, CI5).

// WithCIStorage imposta dove stanno artefatti e cache (disco o S3). Senza, gli endpoint che li
// leggono rispondono 503.
func WithCIStorage(b ci.Blobs) Option { return func(o *routerOptions) { o.ciBlobs = b } }

// WithCICapacity imposta i job che girano insieme (CICapacity, CI7), mostrati da GET /admin/ci/queue.
func WithCICapacity(n int) Option { return func(o *routerOptions) { o.ciCapacity = n } }

func ciUnavailable(w http.ResponseWriter, what string, err error) {
	slog.Default().Warn("ci: "+what+" non riuscito", "err", err)
	writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Servizio CI non disponibile.")
}

// requireInstanceAdmin: identità (401) → amministratore d'istanza (403). Come GET /admin/system.
func (s *apiServer) requireInstanceAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
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
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'amministratore dell'installazione.")
		return uuid.Nil, false
	}
	return callerID, true
}

func storageSettingsToAPI(st ci.Settings) openapi.CiStorageSettings {
	return openapi.CiStorageSettings{
		LogRetentionDays: st.LogRetentionDays, ArtifactRetentionDays: st.ArtifactRetentionDays,
		ArtifactMaxBytes: st.ArtifactMaxBytes, CacheQuotaBytes: st.CacheQuotaBytes, CacheUnusedDays: st.CacheUnusedDays,
	}
}

// GetCiStorageSettings implementa GET /admin/ci/storage.
func (s *apiServer) GetCiStorageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r); !ok {
		return
	}
	st, err := ci.LoadSettings(r.Context(), s.pool)
	if err != nil {
		ciUnavailable(w, "lettura delle impostazioni", err)
		return
	}
	writeJSON(w, http.StatusOK, storageSettingsToAPI(st))
}

// UpdateCiStorageSettings implementa PUT /admin/ci/storage.
func (s *apiServer) UpdateCiStorageSettings(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	var in openapi.CiStorageSettings
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	next := ci.Settings{
		LogRetentionDays: in.LogRetentionDays, ArtifactRetentionDays: in.ArtifactRetentionDays,
		ArtifactMaxBytes: in.ArtifactMaxBytes, CacheQuotaBytes: in.CacheQuotaBytes, CacheUnusedDays: in.CacheUnusedDays,
	}
	if field, msg := next.Validate(); field != "" {
		writeFieldError(w, field, msg)
		return
	}
	saved, err := ci.SaveSettings(r.Context(), s.pool, s.now(), userID, next)
	if err != nil {
		ciUnavailable(w, "salvataggio delle impostazioni", err)
		return
	}
	writeJSON(w, http.StatusOK, storageSettingsToAPI(saved))
}

// GetCiQueue implementa GET /admin/ci/queue: coda, job in corso, concorrenza, attesa media e spazio occupato.
func (s *apiServer) GetCiQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r); !ok {
		return
	}
	stats, err := ci.ReadQueueStats(r.Context(), s.pool, s.now())
	if err != nil {
		ciUnavailable(w, "lettura della coda", err)
		return
	}
	capacity := s.ciCapacity
	if capacity < 1 {
		capacity = ci.DefaultCapacity
	}
	out := openapi.CiQueue{QueuedJobs: stats.Queued, RunningJobs: stats.Running, MaxConcurrentJobs: capacity}
	wait, logB, artB, cacheB := stats.AvgWaitSeconds, stats.LogBytes, stats.ArtifactBytes, stats.CacheBytes
	out.AvgQueueWaitSeconds, out.LogBytes, out.ArtifactBytes, out.CacheBytes = &wait, &logB, &artB, &cacheB
	// I limiti di risorse di un job sono quelli di ci.MaxCPU, ci.MaxMemory e ci.MaxEphemeral (ADR 0001 §3: 2 CPU, 4 GiB, 20 GiB).
	out.ResourceLimits.CpuCores = 2
	out.ResourceLimits.MemoryBytes = 4<<30
	out.ResourceLimits.EphemeralStorageBytes = 20<<30
	out.ResourceLimits.MaxDurationSeconds = int(ci.JobMaxDuration.Seconds())
	writeJSON(w, http.StatusOK, out)
}

// ListWorkflowArtifacts implementa GET /repos/{owner}/{repo}/actions/runs/{runId}/artifacts (read sul repo).
func (s *apiServer) ListWorkflowArtifacts(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.RunIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	list, err := ci.ListArtifacts(r.Context(), s.pool, s.now(), ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Esecuzione non trovata.")
		return
	}
	if err != nil {
		ciUnavailable(w, "elenco degli artefatti", err)
		return
	}
	out := openapi.ArtifactList{Items: make([]openapi.Artifact, 0, len(list))}
	for _, a := range list {
		out.Items = append(out.Items, openapi.Artifact{Id: openapi_types.UUID(a.ID), Name: a.Name, SizeBytes: a.SizeBytes, ExpiresAt: a.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// DownloadWorkflowArtifact implementa GET /repos/{owner}/{repo}/actions/artifacts/{artifactId}/download (read sul repo).
func (s *apiServer) DownloadWorkflowArtifact(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.ArtifactIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if s.ciBlobs == nil {
		writeError(w, http.StatusServiceUnavailable, "ci_storage_unavailable", "L'archiviazione di artefatti e cache non è configurata.")
		return
	}
	a, err := ci.GetArtifact(r.Context(), s.pool, s.now(), ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Artefatto non trovato.")
		return
	}
	if err != nil {
		ciUnavailable(w, "lettura dell'artefatto", err)
		return
	}
	rc, err := s.ciBlobs.Open(r.Context(), a.StorageKey)
	if errors.Is(err, ci.ErrBlobNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Artefatto non trovato.")
		return
	}
	if err != nil {
		ciUnavailable(w, "apertura dell'artefatto", err)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFileName(a.Name)+`.zip"`)
	w.Header().Set("Content-Length", strconv.FormatInt(a.SizeBytes, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// safeFileName tiene lettere, cifre e . _ - (il resto diventa _): il nome dell'artefatto lo sceglie il workflow.
func safeFileName(s string) string {
	b := []rune(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}

func cacheToAPI(c ci.Cache) openapi.ActionsCache {
	return openapi.ActionsCache{Id: openapi_types.UUID(c.ID), Key: c.Key, Ref: c.Ref, SizeBytes: c.SizeBytes, CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt}
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListActionsCaches implementa GET /repos/{owner}/{repo}/actions/caches (read sul repo).
func (s *apiServer) ListActionsCaches(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, p openapi.ListActionsCachesParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := ci.LoadSettings(ctx, s.pool)
	if err != nil {
		ciUnavailable(w, "lettura delle impostazioni", err)
		return
	}
	list, err := ci.ListCaches(ctx, s.pool, ia.repo.ID, strOrEmpty(p.Key), strOrEmpty(p.Ref))
	if err != nil {
		ciUnavailable(w, "elenco delle cache", err)
		return
	}
	used, err := ci.CacheUsedBytes(ctx, s.pool, ia.repo.ID)
	if err != nil {
		ciUnavailable(w, "uso delle cache", err)
		return
	}
	out := openapi.ActionsCacheList{Items: make([]openapi.ActionsCache, 0, len(list)), UsedBytes: used, QuotaBytes: st.CacheQuotaBytes}
	for _, c := range list {
		out.Items = append(out.Items, cacheToAPI(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetActionsCache implementa GET /repos/{owner}/{repo}/actions/caches/{cacheId} (read sul repo).
func (s *apiServer) GetActionsCache(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.CacheIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	c, err := ci.GetCache(r.Context(), s.pool, ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Cache non trovata.")
		return
	}
	if err != nil {
		ciUnavailable(w, "lettura della cache", err)
		return
	}
	writeJSON(w, http.StatusOK, cacheToAPI(c))
}

// ciWriteAccess: read (404) → write (403) → repo non archiviato (409), come il resto delle modifiche della CI.
func (s *apiServer) ciWriteAccess(w http.ResponseWriter, r *http.Request, owner, name string) (issueAccess, bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return ia, false
	}
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Serve write sul repository.")
		return ia, false
	}
	if ia.rejectArchived(w) {
		return ia, false
	}
	if s.ciBlobs == nil {
		writeError(w, http.StatusServiceUnavailable, "ci_storage_unavailable", "L'archiviazione di artefatti e cache non è configurata.")
		return ia, false
	}
	return ia, true
}

// DeleteActionsCaches implementa DELETE /repos/{owner}/{repo}/actions/caches (write sul repo).
func (s *apiServer) DeleteActionsCaches(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, p openapi.DeleteActionsCachesParams) {
	ia, ok := s.ciWriteAccess(w, r, owner, name)
	if !ok {
		return
	}
	n, err := ci.DeleteCaches(r.Context(), s.pool, s.ciBlobs, ia.repo.ID, strOrEmpty(p.Key), strOrEmpty(p.Ref))
	if err != nil {
		ciUnavailable(w, "eliminazione delle cache", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// DeleteActionsCache implementa DELETE /repos/{owner}/{repo}/actions/caches/{cacheId} (write sul repo).
func (s *apiServer) DeleteActionsCache(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.CacheIdParam) {
	ia, ok := s.ciWriteAccess(w, r, owner, name)
	if !ok {
		return
	}
	err := ci.DeleteCache(r.Context(), s.pool, s.ciBlobs, ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Cache non trovata.")
		return
	}
	if err != nil {
		ciUnavailable(w, "eliminazione della cache", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
