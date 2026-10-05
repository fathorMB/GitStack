package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"
)

// Eliminazione recuperabile (R2, R12): il repo sparisce subito per tutti (la
// riga resta, con deleted_at; git lo sposta nel cestino, quindi clone e push
// lo vedono inesistente) e per 7 giorni si può ripristinare. Il nome resta
// occupato. La cancellazione definitiva è del job in internal/repopurge.

// requireAdmin risponde 403 a chi legge ma non è admin, 404 a chi non legge
// nemmeno (per non rivelare l'esistenza). Ritorna false dopo aver risposto.
func (s *apiServer) requireAdmin(w http.ResponseWriter, r *http.Request, userID, repoID uuid.UUID) bool {
	isAdmin, err := s.repoIdentity.HasRole(r.Context(), userID, repoID, "admin")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return false
	}
	if isAdmin {
		return true
	}
	canRead, err := s.repoIdentity.HasRole(r.Context(), userID, repoID, "read")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return false
	}
	if canRead {
		writeError(w, http.StatusForbidden, "forbidden", "Servono i permessi di admin sul repo.")
	} else {
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
	}
	return false
}

// DeleteRepository implementa DELETE /repos/{owner}/{repo}.
func (s *apiServer) DeleteRepository(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.reposReady(w) {
		return
	}
	found, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return
	}
	if !s.requireAdmin(w, r, userID, found.ID) {
		return
	}

	tx, err := s.resources.BeginRepo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	cur, err := tx.Lock(r.Context(), found.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) { // eliminato nel frattempo
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	deleted, err := tx.MarkDeleted(r.Context(), cur.ID, s.now())
	if err != nil {
		slog.Default().Error("eliminazione del repo non riuscita", "repo_id", cur.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	// Git per ultimo prima del commit: se non risponde il repo resta com'era.
	// Già nel cestino (409) o già assente su disco (404) è lo stato voluto.
	if err := s.git.Trash(r.Context(), caller, cur.ID); err != nil &&
		!errors.Is(err, gitclient.ErrConflict) && !errors.Is(err, gitclient.ErrNotFound) {
		slog.Default().Warn("repo non spostato nel cestino da git: eliminazione annullata", "repo_id", cur.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile: il repo non è stato eliminato.")
		return
	}
	if err := emitRepo(r.Context(), tx.Tx(), domainevents.RepositoryDeleted, deleted, userID, func(p *domainevents.RepositoryPayload) {
		if deleted.DeletedAt != nil {
			p.DeletedAt = deleted.DeletedAt.UTC().Format(time.RFC3339)
			p.PurgeAt = store.PurgeAt(*deleted.DeletedAt).UTC().Format(time.RFC3339)
		}
	}); err != nil {
		slog.Default().Error("scrittura dell'evento di eliminazione non riuscita", "repo_id", cur.ID, "err", err)
		s.gitBestEffort(func(ctx context.Context) error { return s.git.Restore(ctx, caller, cur.ID) }, "ripristino su disco", cur.ID)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Default().Error("commit dell'eliminazione non riuscito", "repo_id", cur.ID, "err", err)
		s.gitBestEffort(func(ctx context.Context) error { return s.git.Restore(ctx, caller, cur.ID) }, "ripristino su disco", cur.ID)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RestoreRepository implementa POST /repos/deleted/{repoId}/restore.
func (s *apiServer) RestoreRepository(w http.ResponseWriter, r *http.Request, repoId openapi_types.UUID) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.reposReady(w) {
		return
	}
	id := uuid.UUID(repoId)
	if _, err := s.resources.GetDeletedRepo(r.Context(), id, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	if !s.requireAdmin(w, r, userID, id) {
		return
	}

	tx, err := s.resources.BeginRepo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	// Il blocco di riga si serializza col job di pulizia: o il ripristino
	// vince, o la riga è già sparita e qui è 404.
	if _, err := tx.LockDeleted(r.Context(), id, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	repo, err := tx.Restore(r.Context(), id, s.now())
	if err != nil {
		slog.Default().Error("ripristino del repo non riuscito", "repo_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	if err := s.git.Restore(r.Context(), caller, id); err != nil && !errors.Is(err, gitclient.ErrConflict) {
		slog.Default().Warn("repo non ripristinato da git: ripristino annullato", "repo_id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile: il repo non è stato ripristinato.")
		return
	}
	if err := emitRepo(r.Context(), tx.Tx(), domainevents.RepositoryRestored, repo, userID, nil); err != nil {
		slog.Default().Error("scrittura dell'evento di ripristino non riuscita", "repo_id", id, "err", err)
		s.gitBestEffort(func(ctx context.Context) error { return s.git.Trash(ctx, caller, id) }, "cestino su disco", id)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Default().Error("commit del ripristino non riuscito", "repo_id", id, "err", err)
		s.gitBestEffort(func(ctx context.Context) error { return s.git.Trash(ctx, caller, id) }, "cestino su disco", id)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il ripristino del repo.")
		return
	}
	writeJSON(w, http.StatusOK, s.toAPIRepo(r, repo, s.isEmpty(r.Context(), caller, id)))
}

// ListDeletedRepositories implementa GET /repos/deleted: i repo eliminati da
// meno di 7 giorni su cui il chiamante è admin.
func (s *apiServer) ListDeletedRepositories(w http.ResponseWriter, r *http.Request, params openapi.ListDeletedRepositoriesParams) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sui repo.")
		return
	}
	var owner *string
	if params.Owner != nil {
		o := *params.Owner
		owner = &o
	}
	repos, err := s.resources.ListDeletedRepos(r.Context(), owner, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura dei repo eliminati.")
		return
	}
	items := make([]openapi.DeletedRepository, 0, len(repos))
	for _, rp := range repos {
		isAdmin, err := s.repoIdentity.HasRole(r.Context(), userID, rp.ID, "admin")
		if err != nil {
			slog.Default().Warn("verifica del permesso non riuscita", "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
			return
		}
		if !isAdmin || rp.DeletedAt == nil {
			continue
		}
		items = append(items, openapi.DeletedRepository{
			Id:        openapi_types.UUID(rp.ID),
			Owner:     openapi.RepoOwner{Type: openapi.OwnerType(rp.OwnerType), Name: rp.OwnerName},
			Name:      rp.Name,
			DeletedAt: *rp.DeletedAt,
			PurgeAt:   store.PurgeAt(*rp.DeletedAt),
		})
	}
	writeJSON(w, http.StatusOK, openapi.DeletedRepositoryList{Items: items})
}

// gitBestEffort annulla su disco un passo già fatto quando il commit in core
// è fallito, con un contesto proprio.
func (s *apiServer) gitBestEffort(f func(context.Context) error, what string, id uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f(ctx); err != nil {
		slog.Default().Error("annullamento su disco non riuscito: stato da riallineare", "passo", what, "repo_id", id, "err", err)
	}
}
