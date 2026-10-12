package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// Mirror in pull (M-09, MP1-MP8, GIT-195): il contratto e lo schema
// (migrazione 0015) sono fissati, le operazioni rispondono 501 finché gli item
// a valle (motore, import, UI) non le implementano. Regole: MP1-MP8 in
// .prisma/knowledge/topics/mirror-in-pull.md; gli stati sono quelli del mirror
// in push (mirrors.go).

func pullMirrorNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Mirror in pull non ancora disponibile.")
}

func (s *apiServer) ImportRepository(w http.ResponseWriter, _ *http.Request) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) GetRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) EnableRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) UpdateRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) SyncRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) PauseRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) ResumeRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) StopRepoPullMirror(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullMirrorNotImplemented(w)
}

func (s *apiServer) ListRepoPullMirrorRuns(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepoPullMirrorRunsParams) {
	pullMirrorNotImplemented(w)
}

// fillRepoMirrors imposta `mirror` (MP2, MP7) sui repo di un elenco che hanno
// un mirror in pull attivo (anche in pausa; non interrotto), con una sola
// query. Un errore di lettura lascia il campo vuoto e si logga: i metadati
// del repo restano leggibili.
func (s *apiServer) fillRepoMirrors(ctx context.Context, repos []openapi.Repository) {
	if s.pool == nil || len(repos) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(repos))
	for _, r := range repos {
		if r.Id != nil {
			ids = append(ids, uuid.UUID(*r.Id))
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT repo_id, origin_url, last_success_at FROM core.repo_pull_mirrors
		WHERE repo_id = ANY($1) AND state <> 'stopped'`, ids)
	if err != nil {
		slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", err)
		return
	}
	defer rows.Close()
	found := make(map[uuid.UUID]*openapi.RepositoryMirror, len(ids))
	for rows.Next() {
		var id uuid.UUID
		m := &openapi.RepositoryMirror{}
		if err := rows.Scan(&id, &m.Origin, &m.LastSyncedAt); err != nil {
			slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", err)
			return
		}
		found[id] = m
	}
	if rows.Err() != nil {
		slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", rows.Err())
		return
	}
	for i := range repos {
		if repos[i].Id != nil {
			repos[i].Mirror = found[uuid.UUID(*repos[i].Id)]
		}
	}
}

// repoWithMirror è toAPIRepo più il campo `mirror`.
func (s *apiServer) repoWithMirror(req *http.Request, r store.Repo, empty bool) openapi.Repository {
	one := []openapi.Repository{s.toAPIRepo(req, r, empty)}
	s.fillRepoMirrors(req.Context(), one)
	return one[0]
}
