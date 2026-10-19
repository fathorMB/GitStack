package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Letture delle Pull Request (GIT-261): elenco con ricerca, commit, file e
// cronologia. Chi può leggere il repo legge; il resto è 404.

func pagination(w http.ResponseWriter, page, perPage *int) (int, int, bool) {
	p, pp := defaultPage, defaultPerPage
	if page != nil {
		p = *page
	}
	if perPage != nil {
		pp = *perPage
	}
	if p < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return 0, 0, false
	}
	if pp < 1 || pp > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return 0, 0, false
	}
	return p, pp, true
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListPullRequests implementa GET /repos/{owner}/{repo}/pulls.
func (s *apiServer) ListPullRequests(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListPullRequestsParams) {
	var sort *string
	if params.Sort != nil {
		v := string(*params.Sort)
		sort = &v
	}
	p, ok := parsePullSearch(w, params.Q, sort, params.Page, params.PerPage)
	if !ok {
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if params.State != nil {
		switch *params.State {
		case openapi.ListPullRequestsParamsStateOpen, openapi.ListPullRequestsParamsStateClosed, openapi.ListPullRequestsParamsStateMerged, openapi.ListPullRequestsParamsStateAll:
			p.state = string(*params.State)
		default:
			writeFieldError(w, "state", "state: open, closed, merged o all.")
			return
		}
	}
	p.head, p.base = params.Head, params.Base
	p.caller, p.repoID = ia.userID, &ia.repo.ID
	rows, _, total, err := s.runPullSearch(r.Context(), p)
	if err != nil {
		writeSearchFailure(w, "ricerca delle Pull Request non riuscita", err)
		return
	}
	res, err := s.pullSearchResponse(r, rows)
	if err != nil {
		writeIssueFailure(w, "composizione delle Pull Request non riuscita", err)
		return
	}
	items := make([]openapi.PullRequestSummary, len(res))
	for i, x := range res {
		items[i] = x.PullRequest
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestList{Items: items, Page: p.page, PerPage: p.perPage, Total: total})
}

// pullSummaries compone le righe dell'elenco; repoOf dà il repo di ogni PR (id PR -> id repo).
func (s *apiServer) pullSummaries(r *http.Request, repoOf map[uuid.UUID]uuid.UUID, prs []prRow) ([]openapi.PullRequestSummary, error) {
	ctx := r.Context()
	ids := make([]uuid.UUID, len(prs))
	for i, p := range prs {
		ids[i] = p.ID
	}
	by, all, err := s.pullReviewers(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	users := append([]uuid.UUID{}, all...)
	for _, p := range prs {
		users = append(users, p.AuthorID)
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}
	items := make([]openapi.PullRequestSummary, 0, len(prs))
	for _, p := range prs {
		sum := openapi.PullRequestSummary{
			Number: p.Number, Title: p.Title, State: openapi.PullRequestState(p.State), Draft: p.Draft,
			Author: dir[p.AuthorID], SourceBranch: p.SourceBranch, TargetBranch: p.TargetBranch,
			RequestedReviewers: []openapi.IssueUser{}, MergedAt: p.MergedAt, ClosedAt: p.ClosedAt,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		for _, id := range by[p.ID] {
			sum.RequestedReviewers = append(sum.RequestedReviewers, dir[id])
		}
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
			WHERE t.pull_request_id = $1`, p.ID).Scan(&sum.CommentCount); err != nil {
			return nil, err
		}
		sum.ReviewDecision, _, _, err = pullReviewState(ctx, s.pool, repoOf[p.ID], p, sha40(p.HeadSha), len(sum.RequestedReviewers))
		if err != nil {
			return nil, err
		}
		if sum.StatusState, err = pullStatusState(ctx, s.pool, repoOf[p.ID], sha40(p.HeadSha)); err != nil {
			return nil, err
		}
		items = append(items, sum)
	}
	return items, nil
}

// pullDiff ritorna il confronto di una PR: i branch vivi se è aperta, gli sha
// congelati altrimenti. ok=false dopo aver risposto.
func (s *apiServer) pullDiff(w http.ResponseWriter, r *http.Request, ia issueAccess, number int64, ignoreWS bool) (prRow, pullCompare, bool) {
	ctx := r.Context()
	p, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok {
		return p, pullCompare{}, false
	}
	g, ok := s.pullGit(w, ia)
	if !ok {
		return p, pullCompare{}, false
	}
	branches, err := g.branches(ctx)
	if err != nil {
		gitFailure(w, err)
		return p, pullCompare{}, false
	}
	base, head, _, ok := p.refs(branches)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Il confronto di questa Pull Request non è più disponibile.")
		return p, pullCompare{}, false
	}
	c, err := g.compare(ctx, base, head, ignoreWS)
	if err != nil {
		gitFailure(w, err)
		return p, pullCompare{}, false
	}
	return p, c, true
}

// ListPullRequestCommits implementa GET .../pulls/{number}/commits.
func (s *apiServer) ListPullRequestCommits(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestCommitsParams) {
	page, perPage := 1, 30
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page >= 1 e perPage da 1 a 100.")
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	_, c, ok := s.pullDiff(w, r, ia, number, false)
	if !ok {
		return
	}
	start := min((page-1)*perPage, len(c.Commits))
	end := min(start+perPage, len(c.Commits))
	items := append([]openapi.CommitSummary{}, c.Commits[start:end]...)
	ptrs := make([]*openapi.CommitSummary, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	s.linkAuthors(r.Context(), ptrs...)
	writeJSON(w, http.StatusOK, openapi.CommitList{Items: items, Page: page, PerPage: perPage, HasMore: end < len(c.Commits) || c.CommitsTruncated})
}

// ListPullRequestFiles implementa GET .../pulls/{number}/files.
func (s *apiServer) ListPullRequestFiles(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestFilesParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ws := params.IgnoreWhitespace != nil && *params.IgnoreWhitespace
	p, c, ok := s.pullDiff(w, r, ia, number, ws)
	if !ok {
		return
	}
	files := c.Files
	if params.Path != nil && *params.Path != "" {
		files = nil
		for _, f := range c.Files {
			if f.Path == *params.Path {
				files = []openapi.FileDiff{f}
			}
		}
		if files == nil {
			writeError(w, http.StatusNotFound, "not_found", "Percorso non trovato nella Pull Request.")
			return
		}
	}
	// PR12: i file Viewed sono personali e restano finché il file non cambia.
	g, _ := s.pullGit(w, ia)
	viewed, err := s.viewedPaths(r.Context(), g, p, ia.userID, c)
	if err != nil {
		writeIssueFailure(w, "lettura dei file Viewed non riuscita", err)
		return
	}
	out := openapi.PullRequestFiles{
		Additions: c.Additions, Deletions: c.Deletions, BaseSha: c.MergeBase, HeadSha: c.HeadSha,
		Files: files, FilesChanged: c.FilesChanged, Truncated: c.Truncated, ViewedPaths: viewed,
	}
	if out.BaseSha == "" {
		out.BaseSha = c.BaseSha
	}
	if ws {
		out.IgnoreWhitespace = &ws
	}
	if c.ListOnly {
		lo := true
		out.ListOnly = &lo
	}
	writeJSON(w, http.StatusOK, out)
}

// ListPullRequestEvents implementa GET .../pulls/{number}/events: la
// cronologia in ordine crescente.
func (s *apiServer) ListPullRequestEvents(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestEventsParams) {
	page, perPage, ok := pagination(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	p, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_events WHERE pull_request_id = $1`, p.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "lettura degli eventi non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT id, type, actor_id, data, created_at FROM core.pull_request_events
		WHERE pull_request_id = $1 ORDER BY created_at, id LIMIT $2 OFFSET $3`, p.ID, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura degli eventi non riuscita", err)
		return
	}
	type evRow struct {
		id    uuid.UUID
		typ   string
		actor *uuid.UUID
		data  []byte
		at    time.Time
	}
	var evs []evRow
	var actors []uuid.UUID
	for rows.Next() {
		var e evRow
		if err := rows.Scan(&e.id, &e.typ, &e.actor, &e.data, &e.at); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura degli eventi non riuscita", err)
			return
		}
		if e.actor != nil {
			actors = append(actors, *e.actor)
		}
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura degli eventi non riuscita", err)
		return
	}
	dir, err := s.resolveUsers(ctx, actors)
	if err != nil {
		writeIssueFailure(w, "risoluzione degli utenti non riuscita", err)
		return
	}
	items := make([]openapi.PullRequestEvent, 0, len(evs))
	for _, e := range evs {
		ev := openapi.PullRequestEvent{Id: openapi_types.UUID(e.id), Type: openapi.PullRequestEventType(e.typ), CreatedAt: e.at}
		if e.actor != nil {
			u := dir[*e.actor]
			ev.Actor = &u
		}
		var data map[string]any
		if err := json.Unmarshal(e.data, &data); err == nil && len(data) > 0 {
			ev.Data = &data
		}
		items = append(items, ev)
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestEventList{Items: items, Page: page, PerPage: perPage, Total: total})
}
