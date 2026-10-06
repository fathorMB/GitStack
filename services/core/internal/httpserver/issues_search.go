package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/pkg/issuequery"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Ricerca delle issues (M-05/F, GIT-106, regola I10). Una sola traduzione
// della sintassi in SQL serve sia GET /repos/{owner}/{repo}/issues sia
// GET /search/issues. Ogni valore dell'utente va in un parametro ($n), mai
// nel testo SQL. Come si limita il costo: README di core, «Ricerca delle issues».

const (
	// searchTimeout è il tempo massimo di una ricerca (statement_timeout).
	searchTimeout = 5 * time.Second
	// maxSearchTotal limita il conteggio dei risultati (e quindi la
	// profondità della paginazione): oltre, total vale il tetto.
	maxSearchTotal = 10000
	// maxAgentCandidates limita gli utenti da classificare per @agents.
	maxAgentCandidates = 5000
	// maxHiddenRepoChecks limita le verifiche admin sui repo con issues nascoste.
	maxHiddenRepoChecks = 200
	maxSearchQueryLen   = 512
)

// searchBuilder accumula condizioni e parametri di una ricerca.
type searchBuilder struct {
	s      *apiServer
	ctx    context.Context
	caller uuid.UUID
	where  []string
	args   []any
	agents map[uuid.UUID]bool // cache di @agents; nil = non ancora calcolato
	// scope e scopeArgs: condizioni di ambito (repo, visibilita') prima di
	// quelle dell'utente.
	scope     string
	scopeArgs []any
}

func (b *searchBuilder) arg(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *searchBuilder) text(v string) string { return b.arg(v) + "::text" }

func (b *searchBuilder) add(c string) { b.where = append(b.where, c) }

// cond è una condizione positiva (SQL già parametrizzato).
type cond struct {
	neg bool
	sql string
}

// addGroup aggiunge le condizioni di un qualificatore: i positivi si
// combinano con OR (orPositive) o AND; i negati sono sempre in AND.
func (b *searchBuilder) addGroup(cs []cond, orPositive bool) {
	var pos []string
	for _, c := range cs {
		if c.neg {
			b.add("NOT COALESCE((" + c.sql + "), false)")
		} else {
			pos = append(pos, "COALESCE(("+c.sql+"), false)")
		}
	}
	if len(pos) == 0 {
		return
	}
	sep := " AND "
	if orPositive {
		sep = " OR "
	}
	b.add("(" + strings.Join(pos, sep) + ")")
}

// actorIDs risolve un riferimento a utente in id (nil = nessuno).
func (b *searchBuilder) actorIDs(a issuequery.Actor) ([]uuid.UUID, error) {
	switch a.Kind {
	case issuequery.ActorMe:
		return []uuid.UUID{b.caller}, nil
	case issuequery.ActorAgents:
		if b.agents == nil {
			if err := b.loadAgents(); err != nil {
				return nil, err
			}
		}
		ids := make([]uuid.UUID, 0, len(b.agents))
		for id := range b.agents {
			ids = append(ids, id)
		}
		return ids, nil
	}
	o, err := b.s.repoIdentity.ResolveOwner(b.ctx, a.Login)
	if errors.Is(err, identityclient.ErrNotFound) || (err == nil && o.Type != "user") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUsersUnavailable, err)
	}
	return []uuid.UUID{o.ID}, nil
}

// loadAgents trova gli utenti di tipo agent fra quelli che compaiono come
// autori o assegnatari nell'ambito della ricerca (le condizioni già
// aggiunte: repo, visibilità).
func (b *searchBuilder) loadAgents() error {
	scope := b.scope
	rows, err := b.s.pool.Query(b.ctx, `SELECT uid FROM (
		SELECT i.author_id AS uid FROM core.issues i JOIN core.repositories r ON r.resource_id = i.repo_id WHERE `+scope+`
		UNION
		SELECT a.user_id FROM core.issue_assignees a JOIN core.issues i ON i.id = a.issue_id
			JOIN core.repositories r ON r.resource_id = i.repo_id WHERE `+scope+`) u LIMIT `+strconv.Itoa(maxAgentCandidates), b.scopeArgs...)
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

const (
	hasLabelSQL     = `EXISTS (SELECT 1 FROM core.issue_labels il JOIN core.labels l ON l.id = il.label_id WHERE il.issue_id = i.id`
	hasAssigneeSQL  = `EXISTS (SELECT 1 FROM core.issue_assignees a WHERE a.issue_id = i.id`
	commentCountSQL = `(SELECT count(*) FROM core.issue_comments c WHERE c.issue_id = i.id AND c.deleted_at IS NULL)`
)

// translate aggiunge le condizioni della query. Semantica dei qualificatori
// ripetuti: is, label, assegnatario e no: devono valere tutti (AND); reason,
// autore, milestone, repo e org sono alternative (OR); ogni negazione esclude.
func (b *searchBuilder) translate(q issuequery.Query) error {
	var cs []cond
	for _, c := range q.Is {
		switch c.Value {
		case issuequery.IsIssue:
			cs = append(cs, cond{c.Negated, "TRUE"})
		default:
			cs = append(cs, cond{c.Negated, "i.state = " + b.text(string(c.Value))})
		}
	}
	b.addGroup(cs, false)

	cs = nil
	for _, c := range q.Reasons {
		cs = append(cs, cond{c.Negated, "i.close_reason = " + b.text(strings.ReplaceAll(string(c.Value), "-", "_"))})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, c := range q.Labels {
		cs = append(cs, cond{c.Negated, hasLabelSQL + " AND lower(l.name) = lower(" + b.text(c.Value) + "))"})
	}
	b.addGroup(cs, false)

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
		cs = append(cs, cond{c.Negated, hasAssigneeSQL + " AND a.user_id = ANY(" + b.arg(ids) + "::uuid[]))"})
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
		cs = append(cs, cond{c.Negated, "i.author_id = ANY(" + b.arg(ids) + "::uuid[])"})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, c := range q.Milestones {
		match := "lower(m.title) = lower(" + b.text(c.Value) + ")"
		if n, err := strconv.ParseInt(c.Value, 10, 64); err == nil && n >= 1 {
			match += " OR m.number = " + b.arg(n) + "::bigint"
		}
		cs = append(cs, cond{c.Negated, "EXISTS (SELECT 1 FROM core.milestones m WHERE m.id = i.milestone_id AND (" + match + "))"})
	}
	b.addGroup(cs, true)

	cs = nil
	for _, c := range q.No {
		switch c.Value {
		case issuequery.NoLabel:
			cs = append(cs, cond{c.Negated, "NOT " + hasLabelSQL + ")"})
		case issuequery.NoAssignee:
			cs = append(cs, cond{c.Negated, "NOT " + hasAssigneeSQL + ")"})
		case issuequery.NoMilestone:
			cs = append(cs, cond{c.Negated, "i.milestone_id IS NULL"})
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

	// Testo libero: ricerca testuale PostgreSQL su titolo, testo e commenti.
	// plainto/phraseto_tsquery non interpretano operatori: il testo
	// dell'utente resta testo.
	cs = nil
	for _, t := range q.Text {
		fn := "plainto_tsquery"
		if t.Quoted {
			fn = "phraseto_tsquery"
		}
		tsq := fn + "('simple', " + b.text(t.Text) + ")"
		cs = append(cs, cond{t.Negated, "i.search @@ " + tsq + ` OR EXISTS (SELECT 1 FROM core.issue_comments c
			WHERE c.issue_id = i.id AND c.deleted_at IS NULL AND c.search @@ ` + tsq + ")"})
	}
	b.addGroup(cs, false)
	return nil
}

// orderBy dà l'ORDER BY (sempre con spareggio stabile). relevance richiede
// testo libero non negato.
func (b *searchBuilder) orderBy(sort string, q issuequery.Query, perRepo bool) (string, bool) {
	switch sort {
	case "", "created":
		if perRepo {
			return "i.number DESC", true
		}
		return "i.created_at DESC, i.id DESC", true
	case "updated":
		return "i.updated_at DESC, i.id DESC", true
	case "comments":
		return commentCountSQL + " DESC, i.id DESC", true
	case "relevance":
		txt := q.FreeText()
		if txt == "" {
			return "", false
		}
		tsq := "plainto_tsquery('simple', " + b.text(txt) + ")"
		return "GREATEST(ts_rank(i.search, " + tsq + "), COALESCE((SELECT max(ts_rank(c.search, " + tsq +
			")) FROM core.issue_comments c WHERE c.issue_id = i.id AND c.deleted_at IS NULL), 0)) DESC, i.id DESC", true
	}
	return "", false
}

type searchRow struct {
	issueRow
	repo string
}

type searchParams struct {
	q                 issuequery.Query
	sort              string
	page, perPage     int
	caller            uuid.UUID
	repoID            *uuid.UUID  // ricerca nel repo
	readable          []uuid.UUID // ricerca globale; con all, ignorato
	all               bool
	canSeeHidden      bool        // repo singolo: admin
	hiddenVisibleRepo []uuid.UUID // globale: repo dove è admin
}

// runSearch esegue la ricerca: ritorna le righe della pagina e il totale
// (con tetto maxSearchTotal).
func (s *apiServer) runSearch(ctx context.Context, p searchParams) ([]searchRow, int, issueViews, error) {
	b := &searchBuilder{s: s, ctx: ctx, caller: p.caller}
	b.add("r.deleted_at IS NULL")
	switch {
	case p.repoID != nil:
		b.add("i.repo_id = " + b.arg(*p.repoID))
		if !p.canSeeHidden {
			b.add("NOT i.hidden")
		}
	case !p.all:
		b.add("i.repo_id = ANY(" + b.arg(p.readable) + "::uuid[])")
		b.add("(NOT i.hidden OR i.repo_id = ANY(" + b.arg(p.hiddenVisibleRepo) + "::uuid[]))")
	}
	b.scope = strings.Join(b.where, " AND ")
	b.scopeArgs = append([]any(nil), b.args...)
	if err := b.translate(p.q); err != nil {
		return nil, 0, issueViews{}, err
	}
	cond := strings.Join(b.where, " AND ")
	countArgs := len(b.args)
	order, _ := b.orderBy(p.sort, p.q, p.repoID != nil)
	from := ` FROM core.issues i JOIN core.repositories r ON r.resource_id = i.repo_id WHERE ` + cond

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, issueViews{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", searchTimeout.Milliseconds())); err != nil {
		return nil, 0, issueViews{}, err
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1`+from+` LIMIT `+strconv.Itoa(maxSearchTotal)+`) t`, b.args[:countArgs]...).Scan(&total); err != nil {
		return nil, 0, issueViews{}, err
	}
	limit, offset := b.arg(p.perPage), b.arg((p.page-1)*p.perPage)
	rows, err := tx.Query(ctx, `SELECT `+issueCols+`, r.owner_name || '/' || r.name`+from+
		` ORDER BY `+order+` LIMIT `+limit+` OFFSET `+offset, b.args...)
	if err != nil {
		return nil, 0, issueViews{}, err
	}
	var list []searchRow
	for rows.Next() {
		var x searchRow
		err := rows.Scan(&x.ID, &x.Number, &x.Title, &x.Body, &x.State, &x.CloseReason, &x.DuplicateOf, &x.AuthorID,
			&x.MilestoneID, &x.Locked, &x.Hidden, &x.Edited, &x.ClosedAt, &x.CreatedAt, &x.UpdatedAt, &x.CommentCount, &x.ViaTokenID, &x.ViaTokenName, &x.repo)
		if err != nil {
			rows.Close()
			return nil, 0, issueViews{}, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, issueViews{}, err
	}
	plain := make([]issueRow, len(list))
	for i, x := range list {
		plain[i] = x.issueRow
	}
	v, err := s.loadViews(ctx, tx, plain)
	if err != nil {
		return nil, 0, issueViews{}, err
	}
	v.commits, err = s.linkedCommitCounts(ctx, tx, p.caller, list)
	if err != nil {
		return nil, 0, issueViews{}, err
	}
	return list, total, v, nil
}

// linkedCommitCounts conta, per ogni issue della pagina, gli sha distinti degli
// eventi commit_linked e closed_by_commit. C1: un commit di un altro repo si
// conta solo se il chiamante legge quel repo (come nella cronologia).
func (s *apiServer) linkedCommitCounts(ctx context.Context, q querier, caller uuid.UUID, list []searchRow) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(list) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(list))
	repoOf := make(map[uuid.UUID]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	rows, err := q.Query(ctx, `SELECT e.issue_id, i.repo_id, e.data->'commit'->>'sha', coalesce(e.data->'commit'->>'repositoryId', '')
		FROM core.issue_events e JOIN core.issues i ON i.id = e.issue_id
		WHERE e.issue_id = ANY($1) AND e.type IN ('commit_linked', 'closed_by_commit')`, ids)
	if err != nil {
		return nil, err
	}
	type ev struct {
		issue uuid.UUID
		sha   string
		repo  string
	}
	var evs []ev
	for rows.Next() {
		var issueID, repoID uuid.UUID
		var sha *string
		var repo string
		if err := rows.Scan(&issueID, &repoID, &sha, &repo); err != nil {
			rows.Close()
			return nil, err
		}
		if sha == nil || *sha == "" {
			continue
		}
		repoOf[issueID] = repoID
		evs = append(evs, ev{issueID, *sha, repo})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]map[string]bool{}
	ok := map[uuid.UUID]bool{}
	for _, e := range evs {
		if e.repo != "" && e.repo != repoOf[e.issue].String() {
			rid, err := uuid.Parse(e.repo)
			if err != nil {
				continue
			}
			allowed, known := ok[rid]
			if !known {
				if allowed, err = s.repoIdentity.HasRole(ctx, caller, rid, "read"); err != nil {
					return nil, fmt.Errorf("permesso read sul repo del commit: %w", err)
				}
				ok[rid] = allowed
			}
			if !allowed {
				continue
			}
		}
		if seen[e.issue] == nil {
			seen[e.issue] = map[string]bool{}
		}
		if !seen[e.issue][e.sha] {
			seen[e.issue][e.sha] = true
			out[e.issue]++
		}
	}
	return out, nil
}

func writeSearchFailure(w http.ResponseWriter, what string, err error) {
	if strings.Contains(err.Error(), "statement timeout") || strings.Contains(err.Error(), "57014") {
		slog.Default().Warn("ricerca delle issues oltre il tempo massimo", "err", err)
		writeError(w, http.StatusServiceUnavailable, "search_timeout", "La ricerca ha superato il tempo massimo: restringila (repo:, org:, is:).")
		return
	}
	writeIssueFailure(w, what, err)
}

// pageParams legge e valida page/perPage.
func pageParams(w http.ResponseWriter, page, perPage *int) (int, int, bool) {
	pg, pp := defaultPage, defaultPerPage
	if page != nil {
		pg = *page
	}
	if perPage != nil {
		pp = *perPage
	}
	if pg < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return 0, 0, false
	}
	if pp < 1 || pp > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return 0, 0, false
	}
	if pg*pp > maxSearchTotal+maxPerPage {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Pagina oltre i primi 10000 risultati: restringi la ricerca.")
		return 0, 0, false
	}
	return pg, pp, true
}

func parseSearchQuery(w http.ResponseWriter, q *string) (issuequery.Query, bool) {
	if q == nil {
		return issuequery.Query{}, true
	}
	if !validSearchText(*q) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "q: testo non valido (UTF-8 senza caratteri nulli).")
		return issuequery.Query{}, false
	}
	if len(*q) > maxSearchQueryLen {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "q: al massimo 512 caratteri.")
		return issuequery.Query{}, false
	}
	parsed, err := issuequery.Parse(*q)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "q non valido: "+err.Error())
		return issuequery.Query{}, false
	}
	return parsed, true
}

// validSearchText: Postgres rifiuta NUL e UTF-8 non valido (sarebbe un 500).
func validSearchText(v string) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}

func validSort(w http.ResponseWriter, sort *string, q issuequery.Query) (string, bool) {
	if sort == nil {
		return "created", true
	}
	if *sort == "relevance" && q.FreeText() == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "sort=relevance richiede testo libero in q.")
		return "", false
	}
	return *sort, true
}

// actorParam interpreta assignee/author espliciti: login, @me, @agents.
func actorParam(v string) (issuequery.Actor, bool) {
	q, err := issuequery.Parse("author:" + strconv.Quote(v))
	if err != nil || len(q.Authors) != 1 {
		return issuequery.Actor{}, false
	}
	return q.Authors[0].Value, true
}

// SearchIssues implementa GET /search/issues: su tutta l'installazione, solo
// nei repo leggibili dal chiamante (readable-resources di identity, visibilità
// interna compresa).
func (s *apiServer) SearchIssues(w http.ResponseWriter, r *http.Request, params openapi.SearchIssuesParams) {
	page, perPage, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	parsed, ok := parseSearchQuery(w, params.Q)
	if !ok {
		return
	}
	var sortS *string
	if params.Sort != nil {
		v := string(*params.Sort)
		sortS = &v
	}
	sortV, ok := validSort(w, sortS, parsed)
	if !ok {
		return
	}
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile filtrare le issues per permesso.")
		return
	}
	ctx := r.Context()
	all, ids, err := s.repoIdentity.ReadableResources(ctx, userID)
	if err != nil {
		slog.Default().Warn("elenco delle risorse leggibili non riuscito", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile filtrare le issues per permesso.")
		return
	}
	p := searchParams{q: parsed, sort: sortV, page: page, perPage: perPage, caller: userID, all: all, readable: ids}
	if p.readable == nil {
		p.readable = []uuid.UUID{}
	}
	p.hiddenVisibleRepo = []uuid.UUID{}
	if !all && len(ids) > 0 {
		p.hiddenVisibleRepo, err = s.adminRepos(ctx, userID, ids)
		if err != nil {
			writeSearchFailure(w, "verifica dei permessi non riuscita", err)
			return
		}
	}
	list, total, v, err := s.runSearch(ctx, p)
	if err != nil {
		writeSearchFailure(w, "ricerca delle issues non riuscita", err)
		return
	}
	items := make([]openapi.IssueSearchResult, 0, len(list))
	for _, x := range list {
		items = append(items, openapi.IssueSearchResult{Repo: x.repo, Issue: v.summary(x.issueRow)})
	}
	writeJSON(w, http.StatusOK, openapi.IssueSearchResultList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// adminRepos ritorna, fra i repo leggibili, quelli con issues nascoste dove
// l'utente è admin: le nascoste si mostrano solo lì (I4). Si verificano solo i
// repo che hanno davvero issues nascoste.
func (s *apiServer) adminRepos(ctx context.Context, userID uuid.UUID, readable []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT repo_id FROM core.issues WHERE hidden AND repo_id = ANY($1::uuid[]) LIMIT $2`, readable, maxHiddenRepoChecks)
	if err != nil {
		return nil, err
	}
	var cand []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		cand = append(cand, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []uuid.UUID{}
	for _, id := range cand {
		ok, err := s.repoIdentity.HasRole(ctx, userID, id, "admin")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errUsersUnavailable, err)
		}
		if ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// ListIssues implementa GET /repos/{owner}/{repo}/issues: la stessa ricerca
// limitata al repo; i filtri espliciti si sommano a q.
func (s *apiServer) ListIssues(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListIssuesParams) {
	page, perPage, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	state := "open"
	if params.State != nil {
		state = string(*params.State)
		if state != "open" && state != "closed" && state != "all" {
			writeError(w, http.StatusBadRequest, "invalid_state", "state: open, closed o all.")
			return
		}
	}
	if params.Reason != nil && !params.Reason.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_reason", "reason: completed, not_planned o duplicate.")
		return
	}
	for _, p := range []*string{params.Labels, params.Assignee, params.Author, params.Milestone} {
		if p != nil && !validSearchText(*p) {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Filtro non valido (UTF-8 senza caratteri nulli).")
			return
		}
	}
	parsed, ok := parseSearchQuery(w, params.Q)
	if !ok {
		return
	}
	// Filtri espliciti: si sommano a q.
	hasState := false
	for _, c := range parsed.Is {
		if c.Value != issuequery.IsIssue {
			hasState = true
		}
	}
	if !hasState && state != "all" {
		parsed.Is = append(parsed.Is, issuequery.Cond[issuequery.IsValue]{Value: issuequery.IsValue(state)})
	}
	if params.Reason != nil {
		parsed.Reasons = append(parsed.Reasons, issuequery.Cond[issuequery.Reason]{Value: issuequery.Reason(strings.ReplaceAll(string(*params.Reason), "_", "-"))})
	}
	if params.Labels != nil {
		for _, ln := range strings.Split(*params.Labels, ",") {
			if ln = strings.TrimSpace(ln); ln != "" {
				parsed.Labels = append(parsed.Labels, issuequery.Cond[string]{Value: ln})
			}
		}
	}
	for _, f := range []struct {
		v   *string
		dst *[]issuequery.Cond[issuequery.Actor]
		no  bool
	}{{params.Assignee, &parsed.Assignees, true}, {params.Author, &parsed.Authors, false}} {
		if f.v == nil || *f.v == "" {
			continue
		}
		if f.no && *f.v == "none" {
			parsed.No = append(parsed.No, issuequery.Cond[issuequery.NoField]{Value: issuequery.NoAssignee})
			continue
		}
		a, ok := actorParam(*f.v)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Utente non valido: atteso un login, @me o @agents.")
			return
		}
		*f.dst = append(*f.dst, issuequery.Cond[issuequery.Actor]{Value: a})
	}
	if params.Milestone != nil && *params.Milestone != "" {
		if *params.Milestone == "none" {
			parsed.No = append(parsed.No, issuequery.Cond[issuequery.NoField]{Value: issuequery.NoMilestone})
		} else {
			parsed.Milestones = append(parsed.Milestones, issuequery.Cond[string]{Value: *params.Milestone})
		}
	}
	var sortS *string
	if params.Sort != nil {
		v := string(*params.Sort)
		sortS = &v
	}
	sortV, ok := validSort(w, sortS, parsed)
	if !ok {
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	repoID := ia.repo.ID
	list, total, v, err := s.runSearch(r.Context(), searchParams{q: parsed, sort: sortV, page: page, perPage: perPage,
		caller: ia.userID, repoID: &repoID, canSeeHidden: ia.admin})
	if err != nil {
		writeSearchFailure(w, "elenco delle issues non riuscito", err)
		return
	}
	items := make([]openapi.IssueSummary, 0, len(list))
	for _, x := range list {
		items = append(items, v.summary(x.issueRow))
	}
	writeJSON(w, http.StatusOK, openapi.IssueList{Items: items, Page: page, PerPage: perPage, Total: total})
}
