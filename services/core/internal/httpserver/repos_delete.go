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

	"github.com/google/uuid"
)

// Eliminazione definitiva e immediata (R2 rivista dal board il 4/11, GIT-530):
// non c'è cestino né ripristino. La riga del repo sparisce nella transazione
// della DELETE, quindi il nome è libero alla risposta; il resto (disco di
// git, immagini del registry, app e volumi, allegati, grant) lo toglie
// internal/repopurge subito dopo, e se un passo non riesce lo riprova in
// background senza che nome e visibilità ne dipendano.

// repoPurgeTimeout è il tempo massimo che la DELETE aspetta la pulizia fuori
// da Postgres; oltre, risponde comunque 204 e finisce il giro in background.
const repoPurgeTimeout = 20 * time.Second

// RepoPurger toglie ciò che di un repo già cancellato da core resta fuori da
// Postgres (implementato da repopurge.Job).
type RepoPurger interface {
	Purge(ctx context.Context, repoID uuid.UUID) error
}

// WithRepoPurger imposta chi pulisce disco, registry, app e grant dopo
// DELETE /repos/{owner}/{repo}. Senza, il repo resta in coda per il giro
// periodico di core.
func WithRepoPurger(p RepoPurger) Option {
	return func(o *routerOptions) { o.repoPurger = p }
}

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
	// L'evento porta lo stato del repo com'era un istante prima.
	if err := emitRepo(r.Context(), tx.Tx(), domainevents.RepositoryDeleted, cur, userID, func(p *domainevents.RepositoryPayload) {
		p.DeletedAt = s.now().UTC().Format(time.RFC3339)
	}); err != nil {
		slog.Default().Error("scrittura dell'evento di eliminazione non riuscita", "repo_id", cur.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	if err := tx.DeleteRepo(r.Context(), cur); err != nil {
		slog.Default().Error("eliminazione del repo non riuscita", "repo_id", cur.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	// Git per ultimo prima del commit: se non risponde il repo resta com'era.
	// Già nel cestino (409) o già assente su disco (404) è lo stato voluto.
	if err := s.git.Trash(r.Context(), caller, cur.ID); err != nil &&
		!errors.Is(err, gitclient.ErrConflict) && !errors.Is(err, gitclient.ErrNotFound) {
		slog.Default().Warn("repo non spostato da git: eliminazione annullata", "repo_id", cur.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile: il repo non è stato eliminato.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Default().Error("commit dell'eliminazione non riuscito", "repo_id", cur.ID, "err", err)
		s.gitBestEffort(func(ctx context.Context) error { return s.git.Restore(ctx, caller, cur.ID) }, "ripristino su disco", cur.ID)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione del repo.")
		return
	}
	// Da qui il repo non esiste più e il nome è libero. Il resto si toglie ora;
	// se non riesce resta in coda (repo_cleanup) per il giro periodico.
	if s.repoPurger != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), repoPurgeTimeout)
		defer cancel()
		if err := s.repoPurger.Purge(ctx, cur.ID); err != nil {
			slog.Default().Warn("pulizia del repo eliminato rimandata al giro periodico", "repo_id", cur.ID, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
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
