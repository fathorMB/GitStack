package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/pkg/issuequery"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Ricerca delle Pull Request (PR12a): la sintassi di I10 estesa con is:pr,
// is:draft, is:merged e review:, una sola traduzione in SQL per
// GET /repos/{owner}/{repo}/pulls e GET /search/pulls. Ogni valore dell'utente
// va in un parametro ($n). Riusa il searchBuilder delle issues (stesso
// meccanismo di condizioni, negazioni e risoluzione di @me/@agents).
//
// Per una PR: l'assegnatario è un revisore richiesto; etichette, milestone e
// motivo di chiusura non esistono (le condizioni positive non trovano nulla,
// le negate trovano tutto). review: si calcola come la decisione mostrata
// nell'elenco (pullReviewState), quindi si applica dopo la query SQL.

type pullSearchParams struct {
	q             issuequery.Query
	sort          string // created, updated, comments
	state         string // filtro esplicito: open, closed, merged, all o vuoto
	head, base    *string
	page, perPage int
	caller        uuid.UUID
	repoID        *uuid.UUID  // ricerca nel repo
	readable      []uuid.UUID // ricerca globale; con all, ignorato
	all           bool
}

type pullSearchRow struct {
	prRow
	repoID uuid.UUID
	repo   string
}

const hasReviewerSQL = `EXISTS (SELECT 1 FROM core.pull_request_review_requests rr WHERE rr.pull_request_id = p.id`

// loadPullAgents trova gli utenti di tipo agent fra autori e revisori
// richiesti delle PR nell'ambito della ricerca.
func (b *searchBuilder) loadPullAgents() error {
	rows, err := b.s.pool.Query(b.ctx, `SELECT uid FROM (
		SELECT p.author_id AS uid FROM core.pull_requests p JOIN core.repositories r ON r.resource_id = p.repo_id WHERE `+b.scope+`
		UNION
		SELECT rr.reviewer_id FROM core.pull_request_review_requests rr JOIN core.pull_requests p ON p.id = rr.pull_request_id
			JOIN core.repositories r ON r.resource_id = p.repo_id WHERE `+b.scope+`) u LIMIT `+strconv.Itoa(maxAgentCandidates), b.scopeArgs...)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	b.agents = map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return nil
	}
	look, ok := b.s.repoIdentity.(identityclient.UserLookup)
	if !ok {
		return fmt.Errorf("%w: client senza LookupUsers", errUsersUnavailable)
	}
	found, err := look.LookupUsers(b.ctx, ids)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsersUnavailable, err)
	}
	for id, u := range found {
		if u.Kind == "agent" {
			b.agents[id] = true
		}
	}
	return nil
}

// translatePulls aggiunge le condizioni SQL della query (senza review:).
func (b *searchBuilder) translatePulls(p pullSearchParams) error {
	q := p.q
	var cs []cond
	stateGiven := p.state != ""
	for _, c := range q.Is {
		switch c.Value {
		case issuequery.IsPR:
			cs = append(cs, cond{c.Negated, "TRUE"})
		case issuequery.IsIssue:
			cs = append(cs, cond{c.Negated, "FALSE"})
		case issuequery.IsDraft:
			// una bozza è sempre aperta: -is:draft non toglie il default delle aperte
			stateGiven = stateGiven || !c.Negated
			cs = append(cs, cond{c.Negated, "p.draft AND p.state = 'open'"})
		default:
			stateGiven = true
			cs = append(cs, cond{c.Negated, "p.state = " + b.text(string(c.Value))})
		}
	}
	b.addGroup(cs, false)
	switch p.state {
	case "open", "closed", "merged":
		b.add("p.state = " + b.text(p.state))
	case "":
		// Senza uno stato in q (né esplicito) valgono le PR aperte.
		if !stateGiven {
			b.add("p.state = 'open'")
		}
	}
	if p.head != nil {
		b.add("p.source_branch = " + b.text(*p.head))
	}
	if p.base != nil {
		b.add("p.target_branch = " + b.text(*p.base))
	}

	// Senza etichette, milestone e motivo di chiusura.
	cs = nil
	for _, c := range q.Reasons {
		cs = append(cs, cond{c.Negated, "FALSE"})
	}
	for _, c := range q.Labels {
		cs = append(cs, cond{c.Negated, "FALSE"})
	}
	b.addGroup(cs, false)
	cs = nil
	for _, c := range q.Milestones {
		cs = append(cs, cond{c.Negated, "FALSE"})
	}
	b.addGroup(cs, true)

	// @agents: si risolve una volta, sul perimetro della ricerca.
	needAgents := false
	for _, c := range q.Assignees {
		needAgents = needAgents || c.Value.Kind == issuequery.ActorAgents
	}
	for _, c := range q.Authors {
		needAgents = needAgents || c.Value.Kind == issuequery.ActorAgents
	}
	if needAgents && b.agents == nil {
		if err := b.loadPullAgents(); err != nil {
			return err
		}
	}

	cs = nil
	for _, c := range q.Assignees {
		ids, err := b.actorIDs(c.Value)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			cs = append(cs, cond{c.Negated, "FALSE"})
			continue
		}
		cs = append(cs, cond{c.Negated, hasReviewerSQL + " AND rr.reviewer_id = ANY(" + b.arg(ids) + "::uuid[]))"})
	}
	b.addGroup(cs, false)

	cs = nil
	for _, c := range q.Authors {
		ids, err := b.actorIDs(c.Value)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			cs = append(cs, cond{c.Negated, "FALSE"})
			continue
		}
		cs = append(cs, cond{c.Negated, "p.author_id = ANY(" + b.arg(ids) + "::uuid[])"})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, c := range q.No {
		switch c.Value {
		case issuequery.NoAssignee:
			cs = append(cs, cond{c.Negated, "NOT " + hasReviewerSQL + ")"})
		default: // label, milestone: una PR non ne ha mai
			cs = append(cs, cond{c.Negated, "TRUE"})
		}
	}
	b.addGroup(cs, false)

	cs = nil
	for _, c := range q.Repos {
		cs = append(cs, cond{c.Negated, "lower(r.owner_name) = lower(" + b.text(c.Value.Owner) + ") AND lower(r.name) = lower(" + b.text(c.Value.Name) + ")"})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, c := range q.Orgs {
		cs = append(cs, cond{c.Negated, "r.owner_type = 'organization' AND lower(r.owner_name) = lower(" + b.text(c.Value) + ")"})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, t := range q.Text {
		pat := b.text("%" + likeEscape(t.Text) + "%")
		cs = append(cs, cond{t.Negated, `p.title ILIKE ` + pat + ` ESCAPE '\' OR p.body ILIKE ` + pat + ` ESCAPE '\'`})
	}
	b.addGroup(cs, false)
	return nil
}

func pullOrderBy(sort string) string {
	switch sort {
	case "updated":
		return "p.updated_at DESC, p.number DESC, p.id DESC"
	case "comments":
		return `(SELECT count(*) FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
			WHERE t.pull_request_id = p.id) DESC, p.number DESC, p.id DESC`
	}
	return "p.created_at DESC, p.number DESC, p.id DESC"
}

// matchesReview applica review: alla decisione calcolata (i positivi in AND,
// ogni negazione esclude).
func matchesReview(q issuequery.Query, d openapi.ReviewDecision) bool {
	for _, c := range q.Reviews {
		var want openapi.ReviewDecision
		switch c.Value {
		case issuequery.ReviewRequired:
			want = openapi.ReviewDecisionRequired
		case issuequery.ReviewApproved:
			want = openapi.ReviewDecisionApproved
		case issuequery.ReviewChangesRequested:
			want = openapi.ReviewDecisionChangesRequested
		}
		if (d == want) == c.Negated {
			return false
		}
	}
	return true
}

// runPullSearch esegue la ricerca: la pagina di righe e il totale (con tetto
// maxSearchTotal). Con review: la decisione di ogni candidata si calcola in
// applicazione, quindi la paginazione avviene dopo il filtro.
func (s *apiServer) runPullSearch(ctx context.Context, p pullSearchParams) ([]pullSearchRow, map[uuid.UUID]openapi.ReviewDecision, int, error) {
	b := &searchBuilder{s: s, ctx: ctx, caller: p.caller}
	b.add("r.deleted_at IS NULL")
	switch {
	case p.repoID != nil:
		b.add("p.repo_id = " + b.arg(*p.repoID))
	case !p.all:
		b.add("p.repo_id = ANY(" + b.arg(p.readable) + "::uuid[])")
	}
	b.scope = strings.Join(b.where, " AND ")
	b.scopeArgs = append([]any(nil), b.args...)
	if err := b.translatePulls(p); err != nil {
		return nil, nil, 0, err
	}
	from := ` FROM core.pull_requests p JOIN core.repositories r ON r.resource_id = p.repo_id WHERE ` + strings.Join(b.where, " AND ")
	order := pullOrderBy(p.sort)
	byReview := len(p.q.Reviews) > 0

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", searchTimeout.Milliseconds())); err != nil {
		return nil, nil, 0, err
	}
	total := 0
	args := b.args
	sel := `SELECT ` + prCols + `, p.repo_id, r.owner_name || '/' || r.name` + from + ` ORDER BY ` + order
	if byReview {
		sel += ` LIMIT ` + strconv.Itoa(maxSearchTotal)
	} else {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1`+from+` LIMIT `+strconv.Itoa(maxSearchTotal)+`) t`, args...).Scan(&total); err != nil {
			return nil, nil, 0, err
		}
		args = append(append([]any(nil), args...), p.perPage, (p.page-1)*p.perPage)
		sel += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	}
	rows, err := tx.Query(ctx, sel, args...)
	if err != nil {
		return nil, nil, 0, err
	}
	var list []pullSearchRow
	for rows.Next() {
		var x pullSearchRow
		err := rows.Scan(&x.ID, &x.Number, &x.Title, &x.Body, &x.State, &x.Draft, &x.AuthorID, &x.ViaTokenID, &x.ViaTokenName,
			&x.SourceBranch, &x.TargetBranch, &x.HeadSha, &x.BaseSha, &x.MergeMethod, &x.MergeCommitSha, &x.MergedBy,
			&x.MergedAt, &x.ClosedAt, &x.CreatedAt, &x.UpdatedAt, &x.repoID, &x.repo)
		if err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	if !byReview {
		return list, nil, total, nil
	}
	ids := make([]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	by, _, err := s.pullReviewers(ctx, tx, ids)
	if err != nil {
		return nil, nil, 0, err
	}
	decisions := map[uuid.UUID]openapi.ReviewDecision{}
	var kept []pullSearchRow
	for _, x := range list {
		d, _, _, err := pullReviewState(ctx, tx, x.repoID, x.prRow, sha40(x.HeadSha), len(by[x.ID]))
		if err != nil {
			return nil, nil, 0, err
		}
		if matchesReview(p.q, d) {
			decisions[x.ID] = d
			kept = append(kept, x)
		}
	}
	total = len(kept)
	start := min((p.page-1)*p.perPage, len(kept))
	end := min(start+p.perPage, len(kept))
	return kept[start:end], decisions, total, nil
}

func (s *apiServer) pullSearchResponse(r *http.Request, rows []pullSearchRow) ([]openapi.PullRequestSearchResult, error) {
	prs := make([]prRow, len(rows))
	repoOf := make(map[uuid.UUID]uuid.UUID, len(rows))
	for i, x := range rows {
		prs[i] = x.prRow
		repoOf[x.ID] = x.repoID
	}
	sums, err := s.pullSummaries(r, repoOf, prs)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.PullRequestSearchResult, len(rows))
	for i, x := range rows {
		out[i] = openapi.PullRequestSearchResult{Repo: x.repo, PullRequest: sums[i]}
	}
	return out, nil
}

// parsePullSearch legge q, sort e la paginazione, con gli stessi limiti della
// ricerca delle issues. ok=false dopo aver risposto.
func parsePullSearch(w http.ResponseWriter, q *string, sort *string, page, perPage *int) (pullSearchParams, bool) {
	var p pullSearchParams
	var ok bool
	if p.page, p.perPage, ok = pagination(w, page, perPage); !ok {
		return p, false
	}
	if p.q, ok = parseSearchQuery(w, q); !ok {
		return p, false
	}
	p.sort = "created"
	if sort != nil {
		switch *sort {
		case "created", "updated", "comments":
			p.sort = *sort
		default:
			writeFieldError(w, "sort", "sort: created, updated o comments.")
			return p, false
		}
	}
	return p, true
}

// SearchPullRequests implementa GET /search/pulls: su tutta l'installazione,
// solo nei repo leggibili dal chiamante (readable-resources di identity).
func (s *apiServer) SearchPullRequests(w http.ResponseWriter, r *http.Request, params openapi.SearchPullRequestsParams) {
	var sort *string
	if params.Sort != nil {
		v := string(*params.Sort)
		sort = &v
	}
	p, ok := parsePullSearch(w, params.Q, sort, params.Page, params.PerPage)
	if !ok {
		return
	}
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile filtrare le Pull Request per permesso.")
		return
	}
	ctx := r.Context()
	all, ids, err := s.repoIdentity.ReadableResources(ctx, userID)
	if err != nil {
		slog.Default().Warn("elenco delle risorse leggibili non riuscito", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile filtrare le Pull Request per permesso.")
		return
	}
	p.caller, p.all, p.readable = userID, all, ids
	if p.readable == nil {
		p.readable = []uuid.UUID{}
	}
	rows, _, total, err := s.runPullSearch(ctx, p)
	if err != nil {
		writeSearchFailure(w, "ricerca delle Pull Request non riuscita", err)
		return
	}
	items, err := s.pullSearchResponse(r, rows)
	if err != nil {
		writeIssueFailure(w, "composizione delle Pull Request non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestSearchResultList{Items: items, Page: p.page, PerPage: p.perPage, Total: total})
}
