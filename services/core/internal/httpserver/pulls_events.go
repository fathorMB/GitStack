package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Eventi di dominio delle Pull Request (v1.1, GIT-265, PR11) e chiusura delle
// issues al merge (PR8). Come per le issues l'evento va nell'outbox con la
// transazione della modifica (docs/events.md): notifiche e webhook li leggono
// da lì. L'istantanea della PR (schema PullRequest del contratto) la compone
// emitPull nella stessa transazione, con le letture di git fatte prima
// (pullGitState).

// pullRef è la PR come nei payload degli eventi.
func pullRef(p prRow) domainevents.Pull {
	return domainevents.Pull{ID: p.ID.String(), Number: p.Number, Title: p.Title, State: p.State, Draft: p.Draft,
		AuthorID: p.AuthorID.String(), SourceBranch: p.SourceBranch, TargetBranch: p.TargetBranch, HeadSha: sha40Or(p.HeadSha)}
}

// pullGitBestEffort legge da git ciò che serve all'istantanea. Se git non
// risponde l'azione non fallisce: l'istantanea avrà i contatori a zero.
func (s *apiServer) pullGitBestEffort(ctx context.Context, ia issueAccess, p prRow) pullGitState {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		return pullGitState{}
	}
	gs, err := readPullGit(ctx, pullGit{caller: rd, ia: ia}, p)
	if err != nil {
		slog.Default().Warn("istantanea della PR senza git", "repo", ia.repo.ID, "pull", p.Number, "err", err)
		return pullGitState{}
	}
	return gs
}

// emitPull accoda un evento pull_request.* con l'istantanea della PR com'è
// ora, nella transazione q (la PR si rilegge: riflette la modifica).
func (s *apiServer) emitPull(ctx context.Context, q querier, name string, ia issueAccess, gs pullGitState, number int64,
	actor uuid.UUID, mod func(*domainevents.PullPayload)) error {
	p, err := getPR(ctx, q, ia.repo.ID, number, false)
	if err != nil {
		return fmt.Errorf("lettura della PR per l'evento: %w", err)
	}
	pl := domainevents.PullPayload{Repo: repoSnapshot(ia.repo), Actor: actorOf(ctx, actor), Pull: pullRef(p)}
	// Il nome utente dell'attore, se non è il chiamante, lo risolve il relay.
	snap, err := s.composePull(ctx, q, ia, gs, p)
	if err != nil {
		return fmt.Errorf("istantanea della PR per l'evento: %w", err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	pl.Snapshot = raw
	pl.Pull.HeadSha = snap.HeadSha
	if mod != nil {
		mod(&pl)
	}
	return outbox.Enqueue(ctx, q, name, domainevents.Version, pl)
}

// emitPullLight accoda un evento di sola notifica (senza istantanea).
func (s *apiServer) emitPullLight(ctx context.Context, q outbox.Execer, name string, repo domainevents.Repo, p prRow, actor *domainevents.User,
	mod func(*domainevents.PullPayload)) error {
	pl := domainevents.PullPayload{Repo: repo, Actor: actor, Pull: pullRef(p)}
	if mod != nil {
		mod(&pl)
	}
	return outbox.Enqueue(ctx, q, name, domainevents.Version, pl)
}

// emitPullComment accoda pull_request_comment.created (solo notifiche).
func (s *apiServer) emitPullComment(ctx context.Context, q querier, ia issueAccess, p prRow, commentID uuid.UUID, body string) error {
	return s.emitPullLight(ctx, q, domainevents.PullRequestCommentCreated, repoSnapshot(ia.repo), p, actorOf(ctx, ia.userID),
		func(pl *domainevents.PullPayload) {
			pl.Comment = &domainevents.Comment{ID: commentID.String(), AuthorID: ia.userID.String(), Body: body}
		})
}

// emitReviewRequested accoda un pull_request.review_requested per revisore.
func (s *apiServer) emitReviewRequested(ctx context.Context, q querier, name string, ia issueAccess, gs pullGitState, number int64, reviewers []assignee) error {
	for _, rv := range reviewers {
		rv := rv
		if err := s.emitPull(ctx, q, name, ia, gs, number, ia.userID, func(pl *domainevents.PullPayload) {
			pl.RequestedReviewer = &domainevents.User{ID: rv.id.String(), Username: rv.name}
		}); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// PR8: le issues che la PR chiude al merge

// pullClosingIssues sono i numeri, nel repo della PR, delle issues che le
// parole chiave di C2 chiudono al merge.
func pullClosingIssues(ctx context.Context, q querier, repoID, prID uuid.UUID) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT i.number FROM core.pull_request_closing_issues c JOIN core.issues i ON i.id = c.issue_id
		WHERE c.pull_request_id = $1 AND i.repo_id = $2 ORDER BY i.number`, prID, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// syncPullClosing rilegge le parole chiave di C2 nel testo della PR (PR8):
// collega le issues citate e scorda quelle tolte dal testo. Una issue di un
// altro repo vuole `write` (C1) di chi ha scritto il testo; una nascosta, una
// inesistente o il numero di una PR non si collegano. Per ogni collegamento
// nuovo la issue mostra «PR #n will close this issue» (evento referenced_from
// con willClose).
func (s *apiServer) syncPullClosing(ctx context.Context, tx pgx.Tx, ia issueAccess, p prRow, text string) error {
	type want struct {
		issueID uuid.UUID
		keyword string
		repoID  uuid.UUID
	}
	wanted := map[uuid.UUID]want{}
	repoFull := ia.repo.OwnerName + "/" + ia.repo.Name
	for _, ref := range issuelinks.ParseRefs(text) {
		if ref.Keyword == "" {
			continue
		}
		repoID := ia.repo.ID
		if ref.Repo != "" && ref.Repo != strings.ToLower(repoFull) {
			owner, name, ok := strings.Cut(ref.Repo, "/")
			if !ok {
				continue
			}
			other, err := s.resources.GetRepoByName(ctx, owner, name)
			if err != nil {
				continue
			}
			can, err := s.repoIdentity.HasRole(ctx, ia.userID, other.ID, "write")
			if err != nil {
				return fmt.Errorf("verifica del permesso write: %w", err)
			}
			if !can {
				continue // C1: senza write sul repo della issue non si collega
			}
			repoID = other.ID
		}
		var issueID uuid.UUID
		var hidden bool
		err := tx.QueryRow(ctx, `SELECT id, hidden FROM core.issues WHERE repo_id = $1 AND number = $2`, repoID, ref.Number).Scan(&issueID, &hidden)
		if err != nil {
			continue // inesistente, o il numero di una PR
		}
		if hidden || (repoID == ia.repo.ID && ref.Number == p.Number) {
			continue
		}
		wanted[issueID] = want{issueID: issueID, keyword: ref.Keyword, repoID: repoID}
	}
	// Collegamenti scomparsi dal testo.
	rows, err := tx.Query(ctx, `SELECT issue_id FROM core.pull_request_closing_issues WHERE pull_request_id = $1`, p.ID)
	if err != nil {
		return err
	}
	existing := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id := range existing {
		if _, keep := wanted[id]; !keep {
			if _, err := tx.Exec(ctx, `DELETE FROM core.pull_request_closing_issues WHERE pull_request_id = $1 AND issue_id = $2`, p.ID, id); err != nil {
				return err
			}
		}
	}
	ids := make([]uuid.UUID, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		w := wanted[id]
		if existing[id] {
			if _, err := tx.Exec(ctx, `UPDATE core.pull_request_closing_issues SET keyword = $3 WHERE pull_request_id = $1 AND issue_id = $2`, p.ID, id, w.keyword); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_closing_issues (pull_request_id, issue_id, keyword) VALUES ($1, $2, $3)`,
			p.ID, id, w.keyword); err != nil {
			return err
		}
		err := insertIssueEvent(ctx, tx, id, "referenced_from", ia.userID, map[string]any{
			"source":       map[string]any{"kind": "pull_request", "repository": repoFull, "number": p.Number, "title": p.Title},
			"sourceRepoId": ia.repo.ID.String(), "willClose": true, "keyword": w.keyword,
		})
		if err != nil {
			return fmt.Errorf("scrittura dell'evento referenced_from: %w", err)
		}
	}
	return nil
}

// closePullIssues chiude, come completate (I2), le issues collegate dalle
// parole chiave quando la PR entra nel branch principale (PR8), con
// qualunque metodo di merge. Una issue già chiusa, riaperta dopo il
// collegamento (come per i commit, C2), di un repo archiviato, o di un
// altro repo dove chi unisce non ha write, si lascia com'è. merger è chi
// unisce: l'azione risulta sua.
func (s *apiServer) closePullIssues(ctx context.Context, tx pgx.Tx, ia issueAccess, p prRow, merger uuid.UUID) error {
	if p.TargetBranch != ia.repo.DefaultBranch {
		return nil // solo il merge nel branch principale chiude (R4, C2)
	}
	rows, err := tx.Query(ctx, `SELECT c.issue_id, i.repo_id, i.number, i.state, r.archived_at IS NOT NULL, c.created_at
		FROM core.pull_request_closing_issues c
		JOIN core.issues i ON i.id = c.issue_id
		JOIN core.repositories r ON r.resource_id = i.repo_id
		WHERE c.pull_request_id = $1 ORDER BY c.created_at, c.issue_id`, p.ID)
	if err != nil {
		return err
	}
	type linked struct {
		issueID, repoID uuid.UUID
		number          int64
		state           string
		archived        bool
		linkedAt        time.Time
	}
	var list []linked
	for rows.Next() {
		var t linked
		if err := rows.Scan(&t.issueID, &t.repoID, &t.number, &t.state, &t.archived, &t.linkedAt); err != nil {
			rows.Close()
			return err
		}
		list = append(list, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	repoFull := ia.repo.OwnerName + "/" + ia.repo.Name
	for _, t := range list {
		if t.state != "open" || t.archived {
			continue
		}
		if t.repoID != ia.repo.ID {
			ok, err := s.repoIdentity.HasRole(ctx, merger, t.repoID, "write")
			if err != nil {
				return fmt.Errorf("verifica del permesso write: %w", err)
			}
			if !ok {
				continue
			}
		}
		var reopened bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.issue_events WHERE issue_id = $1 AND type = 'reopened' AND created_at > $2)`,
			t.issueID, t.linkedAt).Scan(&reopened); err != nil {
			return err
		}
		if reopened {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE core.issues SET state = 'closed', close_reason = 'completed', duplicate_of = NULL,
			closed_at = now(), updated_at = now() WHERE id = $1 AND state = 'open'`, t.issueID)
		if err != nil {
			return fmt.Errorf("chiusura della issue: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := insertIssueEvent(ctx, tx, t.issueID, "closed", merger, map[string]any{
			"reason": "completed", "pullRequest": map[string]any{"repository": repoFull, "number": p.Number},
		}); err != nil {
			return fmt.Errorf("evento di chiusura: %w", err)
		}
	}
	return nil
}

// emitReviewSubmitted accoda pull_request_review.submitted con la review del
// contratto (stessa transazione della review).
func (s *apiServer) emitReviewSubmitted(ctx context.Context, tx pgx.Tx, ia issueAccess, gs pullGitState, p prRow, reviewID uuid.UUID, head string) error {
	all, err := s.pullReviews(ctx, tx, p)
	if err != nil {
		return err
	}
	views, err := s.reviewViews(ctx, p, all, head)
	if err != nil {
		return err
	}
	for i, rv := range all {
		if rv.ID != reviewID {
			continue
		}
		raw, err := json.Marshal(views[i])
		if err != nil {
			return err
		}
		return s.emitPull(ctx, tx, domainevents.PullRequestReviewSubmitted, ia, gs, p.Number, ia.userID, func(pl *domainevents.PullPayload) {
			pl.Review = &domainevents.PullReview{ID: rv.ID.String(), AuthorID: rv.AuthorID.String(), State: rv.State, Body: rv.Body}
			pl.ReviewSnapshot = raw
		})
	}
	return fmt.Errorf("review %s assente", reviewID)
}

// afterMerge, nella transazione del merge (PR8, PR11): chiude le issues
// collegate e accoda pull_request.merged. Il merge in git è già fatto e non si
// annulla: ognuno dei due passi gira in un savepoint, se fallisce si scrive
// nel log e il merge resta registrato.
func (s *apiServer) afterMerge(ctx context.Context, tx pgx.Tx, ia issueAccess, gs pullGitState, p prRow) {
	steps := []struct {
		what string
		do   func(q pgx.Tx) error
	}{
		{"chiusura delle issues collegate", func(q pgx.Tx) error { return s.closePullIssues(ctx, q, ia, p, ia.userID) }},
		{"evento pull_request.merged", func(q pgx.Tx) error {
			return s.emitPull(ctx, q, domainevents.PullRequestMerged, ia, gs, p.Number, ia.userID, nil)
		}},
	}
	for _, st := range steps {
		sp, err := tx.Begin(ctx)
		if err != nil {
			slog.Default().Error("savepoint del merge non aperto", "step", st.what, "repo", ia.repo.ID, "pull", p.Number, "err", err)
			continue
		}
		if err := st.do(sp); err != nil {
			_ = sp.Rollback(ctx)
			slog.Default().Error("passo dopo il merge non riuscito", "step", st.what, "repo", ia.repo.ID, "pull", p.Number, "err", err)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			slog.Default().Error("savepoint del merge non chiuso", "step", st.what, "repo", ia.repo.ID, "pull", p.Number, "err", err)
		}
	}
}

// pullBranchesAt elenca i branch la cui punta è sha (al massimo 20), per
// commit_status. Senza git: nessuno.
func (s *apiServer) pullBranchesAt(ctx context.Context, ia issueAccess, sha string) []string {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		return []string{}
	}
	br, err := pullGit{caller: rd, ia: ia}.branches(ctx)
	if err != nil {
		slog.Default().Warn("branch dello stato del commit non letti", "repo", ia.repo.ID, "err", err)
		return []string{}
	}
	out := []string{}
	for name, tip := range br {
		if tip == sha {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// emitCommitStatus accoda commit_status.created e, se uno stato appena
// diventato rosso riguarda un contesto obbligatorio di una PR aperta con
// quel commit in testa, pull_request.checks_failed per l'autore (PR11).
func (s *apiServer) emitCommitStatus(ctx context.Context, tx pgx.Tx, ia issueAccess, x statusRow, prev string, branches []string) error {
	items, err := s.statusesAPI(ctx, []statusRow{x})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(items[0])
	if err != nil {
		return err
	}
	return s.emitCommitStatusRaw(ctx, tx, ia, x, raw, prev, branches)
}

// emitCommitStatusRaw è emitCommitStatus con l'istantanea già composta (raw è il
// CommitStatus del contratto): il Reporter della CI la compone senza leggere
// l'identità, così un'interruzione di identity non blocca la chiusura di un job.
func (s *apiServer) emitCommitStatusRaw(ctx context.Context, tx pgx.Tx, ia issueAccess, x statusRow, raw json.RawMessage,
	prev string, branches []string) error {
	byCtx, err := commitStatusStates(ctx, tx, ia.repo.ID, x.Sha)
	if err != nil {
		return err
	}
	states := make([]string, 0, len(byCtx))
	for _, st := range byCtx {
		states = append(states, st)
	}
	if err := outbox.Enqueue(ctx, tx, domainevents.CommitStatusCreated, domainevents.Version, domainevents.CommitStatusPayload{
		Repo: repoSnapshot(ia.repo), Actor: actorOf(ctx, ia.userID), Status: raw, Sha: x.Sha, Context: x.Context,
		State: x.State, Combined: string(combinedState(states)), Branches: branches,
	}); err != nil {
		return err
	}
	red := func(st string) bool { return st == "failure" || st == "error" }
	if !red(x.State) || red(prev) {
		return nil
	}
	bs, err := loadBranchSettings(ctx, tx, ia.repo.ID)
	if err != nil {
		return err
	}
	required := false
	var failing []string
	for _, c := range bs.contexts {
		if c == x.Context {
			required = true
		}
		if red(byCtx[c]) {
			failing = append(failing, c)
		}
	}
	if !required {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT `+prCols+` FROM core.pull_requests p WHERE p.repo_id = $1 AND p.state = 'open' AND p.head_sha = $2
		ORDER BY p.number`, ia.repo.ID, x.Sha)
	if err != nil {
		return err
	}
	var prs []prRow
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			rows.Close()
			return err
		}
		prs = append(prs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range prs {
		if err := s.emitPullLight(ctx, tx, domainevents.PullRequestChecksFailed, repoSnapshot(ia.repo), p, nil,
			func(pl *domainevents.PullPayload) { pl.Contexts = failing }); err != nil {
			return err
		}
	}
	return nil
}
