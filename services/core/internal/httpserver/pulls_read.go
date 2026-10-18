package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
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

// pullQuery è il filtro di ricerca già interpretato.
type pullQuery struct {
	states   []string
	draft    *bool
	authorID *uuid.UUID
	noMatch  bool
	text     []string
}

// parsePullQuery interpreta `q` (I10 estesa da PR12): is:open|closed|merged,
// is:draft, author:utente|@me e testo libero. Gli altri filtri (review:,
// label:, no:, @agents) non ci sono ancora: 422 invece di ignorarli.
func (s *apiServer) parsePullQuery(r *http.Request, ia issueAccess, q string) (pullQuery, string, error) {
	var pq pullQuery
	for _, tok := range strings.Fields(q) {
		key, val, found := strings.Cut(tok, ":")
		key = strings.ToLower(key)
		if !found || val == "" || (key != "is" && key != "author" && key != "review" && key != "label" && key != "no") {
			pq.text = append(pq.text, tok)
			continue
		}
		switch key {
		case "is":
			switch strings.ToLower(val) {
			case "open", "closed", "merged":
				pq.states = append(pq.states, strings.ToLower(val))
			case "draft":
				t := true
				pq.draft = &t
			case "pr":
			default:
				return pq, fmt.Sprintf("q: is:%s non è un filtro valido.", val), nil
			}
		case "author":
			if strings.EqualFold(val, "@agents") {
				return pq, "q: author:@agents non è ancora supportato.", nil
			}
			if val == "@me" {
				pq.authorID = &ia.userID
				continue
			}
			o, err := s.repoIdentity.ResolveOwner(r.Context(), strings.ToLower(val))
			if errors.Is(err, identityclient.ErrNotFound) || (err == nil && o.Type != "user") {
				pq.noMatch = true
				continue
			}
			if err != nil {
				return pq, "", err
			}
			pq.authorID = &o.ID
		default:
			return pq, fmt.Sprintf("q: il filtro %s: non è ancora supportato.", key), nil
		}
	}
	return pq, "", nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListPullRequests implementa GET /repos/{owner}/{repo}/pulls.
func (s *apiServer) ListPullRequests(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListPullRequestsParams) {
	page, perPage, ok := pagination(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	var pq pullQuery
	if params.Q != nil {
		var msg string
		var err error
		if pq, msg, err = s.parsePullQuery(r, ia, *params.Q); err != nil {
			writeIssueFailure(w, "risoluzione dell'autore non riuscita", fmt.Errorf("%w: %v", errUsersUnavailable, err))
			return
		} else if msg != "" {
			writeFieldError(w, "q", msg)
			return
		}
	}
	conds := []string{"p.repo_id = $1"}
	args := []any{ia.repo.ID}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.Replace(cond, "?", fmt.Sprintf("$%d", len(args)), 1))
	}
	// Lo stato esplicito si somma a quello di q; senza nessuno dei due vale open.
	if params.State != nil {
		switch *params.State {
		case openapi.ListPullRequestsParamsStateOpen, openapi.ListPullRequestsParamsStateClosed, openapi.ListPullRequestsParamsStateMerged:
			add("p.state = ?", string(*params.State))
		case openapi.ListPullRequestsParamsStateAll:
		default:
			writeFieldError(w, "state", "state: open, closed, merged o all.")
			return
		}
	} else if len(pq.states) == 0 && pq.draft == nil {
		add("p.state = ?", "open")
	}
	if len(pq.states) > 0 {
		add("p.state = ANY(?)", pq.states)
	}
	if pq.draft != nil {
		conds = append(conds, "p.draft AND p.state = 'open'")
	}
	if pq.noMatch {
		conds = append(conds, "false")
	}
	if pq.authorID != nil {
		add("p.author_id = ?", *pq.authorID)
	}
	if params.Head != nil {
		add("p.source_branch = ?", *params.Head)
	}
	if params.Base != nil {
		add("p.target_branch = ?", *params.Base)
	}
	for _, t := range pq.text {
		add(`(p.title ILIKE ? ESCAPE '\' OR p.body ILIKE ? ESCAPE '\')`, "%"+likeEscape(t)+"%")
		// il secondo segnaposto riusa lo stesso argomento
		last := len(args)
		conds[len(conds)-1] = strings.Replace(conds[len(conds)-1], "?", fmt.Sprintf("$%d", last), 1)
	}
	order := "p.created_at DESC, p.number DESC"
	if params.Sort != nil {
		switch *params.Sort {
		case openapi.ListPullRequestsParamsSortCreated:
		case openapi.ListPullRequestsParamsSortUpdated:
			order = "p.updated_at DESC, p.number DESC"
		case openapi.ListPullRequestsParamsSortComments:
			order = `(SELECT count(*) FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
				WHERE t.pull_request_id = p.id) DESC, p.number DESC`
		default:
			writeFieldError(w, "sort", "sort: created, updated o comments.")
			return
		}
	}
	where := strings.Join(conds, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_requests p WHERE `+where, args...).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle Pull Request non riuscito", err)
		return
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT `+prCols+` FROM core.pull_requests p WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		where, order, len(args)-1, len(args)), args...)
	if err != nil {
		writeIssueFailure(w, "lettura delle Pull Request non riuscita", err)
		return
	}
	var prs []prRow
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle Pull Request non riuscita", err)
			return
		}
		prs = append(prs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle Pull Request non riuscita", err)
		return
	}
	items, err := s.pullSummaries(r, ia, prs)
	if err != nil {
		writeIssueFailure(w, "composizione delle Pull Request non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestList{Items: items, Page: page, PerPage: perPage, Total: total})
}

func (s *apiServer) pullSummaries(r *http.Request, ia issueAccess, prs []prRow) ([]openapi.PullRequestSummary, error) {
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
		sum.ReviewDecision, _, _, err = pullReviewState(ctx, s.pool, ia.repo.ID, p, sha40(p.HeadSha), len(sum.RequestedReviewers))
		if err != nil {
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
	viewed := []string{}
	// PR12: i file Viewed sono personali e valgono finché la punta non cambia.
	rows, err := s.pool.Query(r.Context(), `SELECT path FROM core.pull_request_viewed_files
		WHERE pull_request_id = $1 AND user_id = $2 AND viewed_sha = $3 ORDER BY path`, p.ID, ia.userID, c.HeadSha)
	if err != nil {
		writeIssueFailure(w, "lettura dei file Viewed non riuscita", err)
		return
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura dei file Viewed non riuscita", err)
			return
		}
		viewed = append(viewed, path)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
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
