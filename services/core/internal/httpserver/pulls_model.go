package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Modello delle Pull Request (v1.1, GIT-261): righe di core.pull_requests,
// letture dal servizio git, revisori, decisione di review e cronologia.
// Regole PR1 e PR6; numerazione condivisa con le issues (I1).

const prCols = `p.id, p.number, p.title, p.body, p.state, p.draft, p.author_id, p.via_token_id, p.via_token_name,
	p.source_branch, p.target_branch, p.head_sha, p.base_sha, p.merge_method, p.merge_commit_sha, p.merged_by,
	p.merged_at, p.closed_at, p.created_at, p.updated_at`

// prRow è una riga di core.pull_requests.
type prRow struct {
	ID             uuid.UUID
	Number         int64
	Title          string
	Body           string
	State          string
	Draft          bool
	AuthorID       uuid.UUID
	ViaTokenID     *uuid.UUID
	ViaTokenName   *string
	SourceBranch   string
	TargetBranch   string
	HeadSha        *string
	BaseSha        *string
	MergeMethod    *string
	MergeCommitSha *string
	MergedBy       *uuid.UUID
	MergedAt       *time.Time
	ClosedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func scanPR(row pgx.Row) (prRow, error) {
	var p prRow
	err := row.Scan(&p.ID, &p.Number, &p.Title, &p.Body, &p.State, &p.Draft, &p.AuthorID, &p.ViaTokenID, &p.ViaTokenName,
		&p.SourceBranch, &p.TargetBranch, &p.HeadSha, &p.BaseSha, &p.MergeMethod, &p.MergeCommitSha, &p.MergedBy,
		&p.MergedAt, &p.ClosedAt, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// getPR legge la PR per numero; store.ErrNotFound se il numero non è una PR
// del repo (per esempio è una issue).
func getPR(ctx context.Context, q querier, repoID uuid.UUID, number int64, forUpdate bool) (prRow, error) {
	sql := `SELECT ` + prCols + ` FROM core.pull_requests p WHERE p.repo_id = $1 AND p.number = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF p`
	}
	p, err := scanPR(q.QueryRow(ctx, sql, repoID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		return prRow{}, store.ErrNotFound
	}
	return p, err
}

func writePRNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Pull Request non trovata.")
}

func writePullArchived(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: le Pull Request sono in sola lettura.")
}

// insertPullEvent scrive un evento nella cronologia della PR (stessa
// transazione dell'azione).
func insertPullEvent(ctx context.Context, q querier, prID uuid.UUID, typ string, actor uuid.UUID, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO core.pull_request_events (id, pull_request_id, type, actor_id, data, created_at)
		VALUES ($1, $2, $3, $4, $5, clock_timestamp())`, uuid.New(), prID, typ, actor, b)
	return err
}

// pullGit è ciò che serve del servizio git.
type pullGit struct {
	caller gitclient.Reader
	ia     issueAccess
}

func (s *apiServer) pullGit(w http.ResponseWriter, ia issueAccess) (pullGit, bool) {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Letture del codice non disponibili.")
		return pullGit{}, false
	}
	return pullGit{caller: rd, ia: ia}, true
}

// branches ritorna nome -> sha della punta di ogni branch del repo.
func (g pullGit) branches(ctx context.Context) (map[string]string, error) {
	raw, err := g.caller.ReadJSON(ctx, g.ia.caller, g.ia.repo.ID, "branches", nil)
	if err != nil {
		return nil, err
	}
	var bl openapi.BranchList
	if err := json.Unmarshal(raw, &bl); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(bl.Items))
	for _, b := range bl.Items {
		out[b.Name] = b.Commit.Sha
	}
	return out, nil
}

// pullCompare è la risposta del confronto del servizio git.
type pullCompare struct {
	BaseSha          string                  `json:"baseSha"`
	HeadSha          string                  `json:"headSha"`
	MergeBase        string                  `json:"mergeBase"`
	AheadBy          int                     `json:"aheadBy"`
	BehindBy         int                     `json:"behindBy"`
	Commits          []openapi.CommitSummary `json:"commits"`
	CommitsTruncated bool                    `json:"commitsTruncated"`
	Files            []openapi.FileDiff      `json:"files"`
	FilesChanged     int                     `json:"filesChanged"`
	Additions        int                     `json:"additions"`
	Deletions        int                     `json:"deletions"`
	Truncated        bool                    `json:"truncated"`
	ListOnly         bool                    `json:"listOnly"`
}

func (g pullGit) compare(ctx context.Context, base, head string, ignoreWS bool) (pullCompare, error) {
	q := url.Values{"base": {base}, "head": {head}}
	if ignoreWS {
		q.Set("ignoreWhitespace", "true")
	}
	raw, err := g.caller.ReadJSON(ctx, g.ia.caller, g.ia.repo.ID, "compare", q)
	if err != nil {
		return pullCompare{}, err
	}
	var c pullCompare
	if err := json.Unmarshal(raw, &c); err != nil {
		return pullCompare{}, err
	}
	return c, nil
}

// refs sceglie i riferimenti del confronto di una PR: i branch vivi se è
// aperta e li ha entrambi, altrimenti gli sha congelati (PR chiusa o unita, o
// un branch eliminato). ok=false se non c'è niente da confrontare.
func (p prRow) refs(branches map[string]string) (base, head string, live, ok bool) {
	if p.State == "open" {
		_, hasSrc := branches[p.SourceBranch]
		_, hasDst := branches[p.TargetBranch]
		if hasSrc && hasDst {
			return p.TargetBranch, p.SourceBranch, true, true
		}
	}
	if p.BaseSha != nil && p.HeadSha != nil {
		return *p.BaseSha, *p.HeadSha, false, true
	}
	return "", "", false, false
}

// pullReviewState calcola approvazioni valide, approvazioni richieste e
// decisione di review. Le approvazioni valide sono quelle dell'ultima review
// di ogni persona, sull'ultimo commit, non dell'autore e non di un agente
// (PR2, PR4); la verifica del permesso write dei revisori sta con la
// scrittura delle review (item a valle).
func pullReviewState(ctx context.Context, q querier, repoID uuid.UUID, p prRow, headSha string, requested int) (decision openapi.ReviewDecision, approvals, required int, err error) {
	if err = q.QueryRow(ctx, `SELECT COALESCE((SELECT required_approvals FROM core.branch_settings
		WHERE repo_id = $1 AND require_pull_request), 0)`, repoID).Scan(&required); err != nil {
		return
	}
	rows, err := q.Query(ctx, `SELECT state, commit_sha, by_agent, author_id FROM (
			SELECT DISTINCT ON (author_id) state, commit_sha, by_agent, author_id
			FROM core.pull_request_reviews WHERE pull_request_id = $1 AND state IN ('approve', 'request_changes') AND NOT by_agent
			ORDER BY author_id, created_at DESC) x`, p.ID)
	if err != nil {
		return
	}
	defer rows.Close()
	changes := 0
	for rows.Next() {
		var state, sha string
		var byAgent bool
		var author uuid.UUID
		if err = rows.Scan(&state, &sha, &byAgent, &author); err != nil {
			return
		}
		switch {
		case state == "request_changes":
			changes++
		case sha == headSha && !byAgent && author != p.AuthorID:
			approvals++
		}
	}
	if err = rows.Err(); err != nil {
		return
	}
	decision = reviewDecision(changes, approvals, required, requested)
	return
}

func reviewDecision(changes, approvals, required, requested int) openapi.ReviewDecision {
	switch {
	case changes > 0:
		return openapi.ReviewDecision("changes_requested")
	case required > 0 && approvals >= required, required == 0 && approvals > 0:
		return openapi.ReviewDecision("approved")
	case required > 0 || requested > 0:
		return openapi.ReviewDecision("required")
	default:
		return openapi.ReviewDecision("none")
	}
}

// pullReviewDecisions calcola la decisione di review di molte PR con due
// query, qualunque sia il numero delle PR (stessa logica di pullReviewState).
// requested: revisori richiesti per PR.
func pullReviewDecisions(ctx context.Context, q querier, prs []pullSearchRow, requested map[uuid.UUID][]uuid.UUID) (map[uuid.UUID]openapi.ReviewDecision, error) {
	out := make(map[uuid.UUID]openapi.ReviewDecision, len(prs))
	if len(prs) == 0 {
		return out, nil
	}
	repoIDs := make([]uuid.UUID, 0, len(prs))
	prIDs := make([]uuid.UUID, 0, len(prs))
	byID := make(map[uuid.UUID]pullSearchRow, len(prs))
	for _, x := range prs {
		repoIDs = append(repoIDs, x.repoID)
		prIDs = append(prIDs, x.ID)
		byID[x.ID] = x
	}
	reqRows, err := q.Query(ctx, `SELECT repo_id, required_approvals FROM core.branch_settings
		WHERE repo_id = ANY($1) AND require_pull_request`, repoIDs)
	if err != nil {
		return nil, err
	}
	required := map[uuid.UUID]int{}
	for reqRows.Next() {
		var id uuid.UUID
		var n int
		if err := reqRows.Scan(&id, &n); err != nil {
			reqRows.Close()
			return nil, err
		}
		required[id] = n
	}
	reqRows.Close()
	if err := reqRows.Err(); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT pull_request_id, state, commit_sha, by_agent, author_id FROM (
			SELECT DISTINCT ON (pull_request_id, author_id) pull_request_id, state, commit_sha, by_agent, author_id
			FROM core.pull_request_reviews WHERE pull_request_id = ANY($1) AND state IN ('approve', 'request_changes') AND NOT by_agent
			ORDER BY pull_request_id, author_id, created_at DESC) x`, prIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := map[uuid.UUID]int{}
	approvals := map[uuid.UUID]int{}
	for rows.Next() {
		var pr, author uuid.UUID
		var state, sha string
		var byAgent bool
		if err := rows.Scan(&pr, &state, &sha, &byAgent, &author); err != nil {
			return nil, err
		}
		x := byID[pr]
		switch {
		case state == "request_changes":
			changes[pr]++
		case sha == sha40(x.HeadSha) && !byAgent && author != x.AuthorID:
			approvals[pr]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, x := range prs {
		out[x.ID] = reviewDecision(changes[x.ID], approvals[x.ID], required[x.repoID], len(requested[x.ID]))
	}
	return out, nil
}

// pullReviewers ritorna i revisori richiesti, nell'ordine della richiesta.
func (s *apiServer) pullReviewers(ctx context.Context, q querier, prIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, []uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT pull_request_id, reviewer_id FROM core.pull_request_review_requests
		WHERE pull_request_id = ANY($1) ORDER BY requested_at, reviewer_id`, prIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	by := map[uuid.UUID][]uuid.UUID{}
	var all []uuid.UUID
	for rows.Next() {
		var pr, u uuid.UUID
		if err := rows.Scan(&pr, &u); err != nil {
			return nil, nil, err
		}
		by[pr] = append(by[pr], u)
		all = append(all, u)
	}
	return by, all, rows.Err()
}

func sha40(s *string) string {
	if s != nil && *s != "" {
		return *s
	}
	return strings.Repeat("0", 40)
}

// buildPull compone la PR della risposta. Con la PR aperta chiede a git
// l'ultimo stato dei branch; per chiusa o unita usa gli sha congelati.
func (s *apiServer) buildPull(ctx context.Context, ia issueAccess, g pullGit, p prRow) (openapi.PullRequest, error) {
	gs, err := readPullGit(ctx, g, p)
	if err != nil {
		return openapi.PullRequest{}, err
	}
	return s.composePull(ctx, s.pool, ia, gs, p)
}

// pullGitState è ciò che la composizione della PR legge da git: se il branch
// di origine esiste e il confronto (commit, file, righe) con la destinazione.
// Si legge prima di aprire una transazione, così composePull può girare dentro
// la transazione di una modifica (eventi di dominio, GIT-265) senza chiamare git.
type pullGitState struct {
	srcExists bool
	haveCmp   bool
	live      bool
	cmp       pullCompare
}

func readPullGit(ctx context.Context, g pullGit, p prRow) (pullGitState, error) {
	branches, err := g.branches(ctx)
	if err != nil {
		return pullGitState{}, err
	}
	_, srcExists := branches[p.SourceBranch]
	gs := pullGitState{srcExists: srcExists}
	if base, head, live, ok := p.refs(branches); ok {
		c, err := g.compare(ctx, base, head, false)
		if err != nil {
			return pullGitState{}, err
		}
		gs.haveCmp, gs.live, gs.cmp = true, live, c
	}
	return gs, nil
}

// composePull compone la PR del contratto con le letture di database fatte su
// q (il pool o la transazione di una modifica) e lo stato di git già letto.
func (s *apiServer) composePull(ctx context.Context, q querier, ia issueAccess, gs pullGitState, p prRow) (openapi.PullRequest, error) {
	var err error
	srcExists := gs.srcExists
	out := openapi.PullRequest{
		Id: openapi_types.UUID(p.ID), Number: p.Number, Title: p.Title, Body: p.Body,
		State: openapi.PullRequestState(p.State), Draft: p.Draft, SourceBranch: p.SourceBranch,
		TargetBranch: p.TargetBranch, SourceBranchExists: srcExists, ClosingIssues: []int64{},
		BaseSha: p.BaseSha, HeadSha: sha40(p.HeadSha), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		MergeCommitSha: p.MergeCommitSha, MergedAt: p.MergedAt, ClosedAt: p.ClosedAt,
	}
	if p.MergeMethod != nil {
		m := openapi.MergeMethod(*p.MergeMethod)
		out.MergeMethod = &m
	}
	if p.ViaTokenID != nil && p.ViaTokenName != nil {
		out.ViaToken = &openapi.IssueViaToken{Id: openapi_types.UUID(*p.ViaTokenID), Name: *p.ViaTokenName}
	}
	if gs.haveCmp {
		c := gs.cmp
		out.CommitCount, out.ChangedFiles, out.Additions, out.Deletions = c.AheadBy, c.FilesChanged, c.Additions, c.Deletions
		if gs.live && p.State == "open" {
			out.HeadSha = c.HeadSha
			if c.MergeBase != "" {
				mb := c.MergeBase
				out.BaseSha = &mb
			}
		}
	}

	by, all, err := s.pullReviewers(ctx, q, []uuid.UUID{p.ID})
	if err != nil {
		return openapi.PullRequest{}, err
	}
	ids := append([]uuid.UUID{p.AuthorID}, all...)
	if p.MergedBy != nil {
		ids = append(ids, *p.MergedBy)
	}
	dir, err := s.resolveUsers(ctx, ids)
	if err != nil {
		return openapi.PullRequest{}, err
	}
	out.Author = dir[p.AuthorID]
	if p.MergedBy != nil {
		u := dir[*p.MergedBy]
		out.MergedBy = &u
	}
	out.RequestedReviewers = []openapi.IssueUser{}
	for _, id := range by[p.ID] {
		out.RequestedReviewers = append(out.RequestedReviewers, dir[id])
	}
	if err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id WHERE t.pull_request_id = $1),
		(SELECT count(*) FROM core.pull_request_threads WHERE pull_request_id = $1 AND NOT resolved)`, p.ID).
		Scan(&out.CommentCount, &out.UnresolvedThreadCount); err != nil {
		return openapi.PullRequest{}, err
	}
	out.ReviewDecision, out.Approvals, out.RequiredApprovals, err = pullReviewState(ctx, q, ia.repo.ID, p, out.HeadSha, len(out.RequestedReviewers))
	if err != nil {
		return out, err
	}
	out.StatusState, err = pullStatusState(ctx, q, ia.repo.ID, out.HeadSha)
	if err == nil {
		out.ClosingIssues, err = pullClosingIssues(ctx, q, ia.repo.ID, p.ID)
	}
	return out, err
}

// respondPull rilegge la PR (dopo il commit) e la scrive nella risposta.
func (s *apiServer) respondPull(w http.ResponseWriter, ctx context.Context, status int, ia issueAccess, number int64, headers map[string]string) {
	g, ok := s.pullGit(w, ia)
	if !ok {
		return
	}
	p, err := getPR(ctx, s.pool, ia.repo.ID, number, false)
	if err != nil {
		writeIssueFailure(w, "lettura della Pull Request non riuscita", err)
		return
	}
	out, err := s.buildPull(ctx, ia, g, p)
	if err != nil {
		s.writePullBuildFailure(w, err)
		return
	}
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	writeJSON(w, status, out)
}

// writePullBuildFailure: errori di git (503/4xx del servizio) o interni.
func (s *apiServer) writePullBuildFailure(w http.ResponseWriter, err error) {
	var ae *gitclient.APIError
	if errors.As(err, &ae) || errors.Is(err, gitclient.ErrUnavailable) {
		gitFailure(w, err)
		return
	}
	writeIssueFailure(w, "composizione della Pull Request non riuscita", err)
}

// pullIsReadOnlyMirror: un repo con un mirror in pull attivo è in sola
// lettura (MP): nessuna PR si apre o si cambia.
func (s *apiServer) pullIsReadOnlyMirror(ctx context.Context, repoID uuid.UUID) (bool, error) {
	var ro bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.repo_pull_mirrors WHERE repo_id = $1 AND state <> 'stopped')`, repoID).Scan(&ro)
	return ro, err
}

// pullMutable risponde 409 se il repo è archiviato o in mirror; ok=true se la
// modifica può proseguire.
func (s *apiServer) pullMutable(w http.ResponseWriter, ctx context.Context, ia issueAccess) bool {
	if ia.repo.ArchivedAt != nil {
		writePullArchived(w)
		return false
	}
	ro, err := s.pullIsReadOnlyMirror(ctx, ia.repo.ID)
	if err != nil {
		writeIssueFailure(w, "verifica del mirror non riuscita", err)
		return false
	}
	if ro {
		writeError(w, http.StatusConflict, "read_only_mirror", "Il repo è un mirror in pull: è in sola lettura.")
		return false
	}
	return true
}

// pullFor trova la PR; un numero che non è una PR è 404.
func (s *apiServer) pullFor(w http.ResponseWriter, ctx context.Context, q querier, ia issueAccess, number int64, forUpdate bool) (prRow, bool) {
	p, err := getPR(ctx, q, ia.repo.ID, number, forUpdate)
	if errors.Is(err, store.ErrNotFound) {
		writePRNotFound(w)
		return prRow{}, false
	}
	if err != nil {
		writeIssueFailure(w, "lettura della Pull Request non riuscita", err)
		return prRow{}, false
	}
	return p, true
}

// beginPullWrite apre la transazione di una modifica: blocca il repo (R10) e
// la PR. Se ritorna ok=false ha già risposto e chiuso la transazione.
func (s *apiServer) beginPullWrite(w http.ResponseWriter, r *http.Request, ia issueAccess, number int64) (pgx.Tx, prRow, bool) {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return nil, prRow{}, false
	}
	if err := lockRepoForWrite(ctx, tx, ia.repo.ID); err != nil {
		_ = tx.Rollback(ctx)
		switch {
		case errors.Is(err, store.ErrArchived):
			writePullArchived(w)
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		default:
			writeIssueFailure(w, "verifica del repo non riuscita", err)
		}
		return nil, prRow{}, false
	}
	p, ok := s.pullFor(w, ctx, tx, ia, number, true)
	if !ok {
		_ = tx.Rollback(ctx)
		return nil, prRow{}, false
	}
	return tx, p, true
}

func pullLocation(ia issueAccess, number int64) string {
	return "/repos/" + ia.repo.OwnerName + "/" + ia.repo.Name + "/pulls/" + strconv.FormatInt(number, 10)
}
