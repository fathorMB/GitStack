package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WithCILogs imposta i log dei job su disco (GIT-316, CI5). Senza, GET .../log
// risponde 503.
func WithCILogs(l *ci.LogStore) Option {
	return func(o *routerOptions) { o.ciLogs = l }
}

// ciLogFollowEvery è ogni quanto `follow=true` guarda se il log è cresciuto.
const ciLogFollowEvery = 500 * time.Millisecond

// GetWorkflowJobLog implementa GET /repos/{owner}/{repo}/actions/jobs/{jobId}/logs:
// il log del job in text/plain, nell'ordine in cui il runner l'ha scritto, anche
// mentre il job gira; con follow=true resta aperto fino alla fine del job.
// Vale il permesso di lettura del repo (come le esecuzioni).
func (s *apiServer) GetWorkflowJobLog(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.JobIdParam, p openapi.GetWorkflowJobLogParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if s.ciLogs == nil {
		writeError(w, http.StatusServiceUnavailable, "ci_logs_unavailable", "I log dei job non sono configurati.")
		return
	}
	ctx := r.Context()
	jobID := uuid.UUID(id)
	job, err := ci.JobRef(ctx, s.pool, ia.repo.ID, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Job non trovato.")
		return
	}
	if err != nil {
		slog.Default().Warn("ci: lettura del job non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Job non disponibile.")
		return
	}
	follow := p.Follow != nil && *p.Follow
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	var offset int64
	for {
		// Lo stato prima della dimensione: se il job è finito, la lettura che segue è l'ultima.
		finished := job.Status == ci.StatusCompleted || job.Status == ci.StatusCancelled
		size, err := s.ciLogs.LogSize(ctx, s.pool, jobID)
		if err != nil {
			slog.Default().Warn("ci: dimensione del log non letta", "err", err)
			return
		}
		if size > offset {
			n, err := s.ciLogs.ReadFrom(jobID, offset, size, w)
			offset += n
			if err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if !follow || finished {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ciLogFollowEvery):
		}
		if job, err = ci.JobRef(ctx, s.pool, ia.repo.ID, jobID); err != nil {
			return
		}
	}
}
