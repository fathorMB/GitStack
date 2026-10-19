package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Le Pull Request davanti a un push (v1.1, GIT-265, PR11), dal consumer
// durevole core-pull-sync di git.push:
//   - nuovi commit sul branch di origine di una PR aperta: pull_request
//     synchronize (webhook) e, per ogni approvazione valida sul commit
//     precedente, pull_request_review dismissed (PR4: un nuovo commit annulla
//     le approvazioni);
//   - ogni push sul branch di origine o di destinazione di una PR aperta
//     ricalcola i conflitti: alla comparsa si avvisa l'autore, una volta sola
//     finché i conflitti non spariscono e tornano.

// NewPullPushHandler costruisce il gitpush.Handler delle PR. Usa le stesse
// opzioni del router (identity, git).
func NewPullPushHandler(pool *pgxpool.Pool, publisher events.Publisher, opts ...Option) func(ctx context.Context, env pkgevents.Envelope, p pkggitpush.Payload) error {
	s := newAPIServer(pool, publisher, opts...)
	return s.handlePullPush
}

func (s *apiServer) handlePullPush(ctx context.Context, _ pkgevents.Envelope, p pkggitpush.Payload) error {
	pusher, err := uuid.Parse(p.Pusher.ID)
	if err != nil {
		return nil // un push senza utente valido non si elabora mai
	}
	owner, name, ok := strings.Cut(p.Repo.FullName, "/")
	if !ok {
		return nil
	}
	repo, err := s.resources.GetRepoByName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if repo.DeletedAt != nil || repo.ArchivedAt != nil {
		return nil
	}
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		return nil // senza git non c'è nulla da confrontare
	}
	ia := issueAccess{repo: repo, userID: pusher, caller: trust.Identity{UserID: pusher.String(), Username: p.Pusher.Username}}
	g := pullGit{caller: rd, ia: ia}
	for _, ref := range p.Refs {
		branch, ok := strings.CutPrefix(ref.Ref, "refs/heads/")
		if !ok || ref.After == pkggitpush.ZeroSHA {
			continue
		}
		if err := s.pullsAfterPush(ctx, ia, g, branch, ref.After); err != nil {
			return fmt.Errorf("PR dopo il push su %s: %w", branch, err)
		}
	}
	return nil
}

// pullsAfterPush elabora le PR aperte che hanno branch come origine o come
// destinazione.
func (s *apiServer) pullsAfterPush(ctx context.Context, ia issueAccess, g pullGit, branch, after string) error {
	rows, err := s.pool.Query(ctx, `SELECT `+prCols+` FROM core.pull_requests p
		WHERE p.repo_id = $1 AND p.state = 'open' AND (p.source_branch = $2 OR p.target_branch = $2) ORDER BY p.number`, ia.repo.ID, branch)
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
		if p.SourceBranch == branch && sha40Or(p.HeadSha) != after {
			if err := s.pullSynchronize(ctx, ia, g, p, after); err != nil {
				return err
			}
		}
		if err := s.pullConflictCheck(ctx, ia, g, p); err != nil {
			return err
		}
	}
	return nil
}

// pullSynchronize registra i nuovi commit di una PR: l'head cambia, le
// approvazioni sul commit precedente sono annullate (webhook dismissed) e parte
// synchronize.
func (s *apiServer) pullSynchronize(ctx context.Context, ia issueAccess, g pullGit, p prRow, after string) error {
	gs, err := readPullGit(ctx, g, p)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := getPR(ctx, tx, ia.repo.ID, p.Number, true)
	if err != nil {
		return err
	}
	if cur.State != "open" || sha40Or(cur.HeadSha) == after {
		return nil // già fatto da un'altra replica, o la PR è cambiata
	}
	old := sha40Or(cur.HeadSha)
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET head_sha = $2, updated_at = now() WHERE id = $1`, cur.ID, after); err != nil {
		return err
	}
	cur.HeadSha = &after
	// Approvazioni valide sul commit precedente (ultima decisiva di una
	// persona, non l'autore): ora sono obsolete.
	reviews, err := s.pullReviews(ctx, tx, cur)
	if err != nil {
		return err
	}
	views, err := s.reviewViews(ctx, cur, reviews, old) // come contavano prima
	if err != nil {
		return err
	}
	afterViews, err := s.reviewViews(ctx, cur, reviews, after)
	if err != nil {
		return err
	}
	if err := s.emitPullAt(ctx, tx, ia, gs, domainevents.PullRequestSynchronize, cur, nil); err != nil {
		return err
	}
	for i, rv := range reviews {
		if !views[i].CountsAsApproval || afterViews[i].CountsAsApproval {
			continue
		}
		raw, err := json.Marshal(afterViews[i])
		if err != nil {
			return err
		}
		rv := rv
		if err := s.emitPullAt(ctx, tx, ia, gs, domainevents.PullRequestReviewDismissed, cur, func(pl *domainevents.PullPayload) {
			pl.Review = &domainevents.PullReview{ID: rv.ID.String(), AuthorID: rv.AuthorID.String(), State: rv.State, Body: rv.Body}
			pl.ReviewSnapshot = raw
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.ciPullEvent(ctx, ia, p.Number, "synchronize")
	return nil
}

// emitPullAt come emitPull, con la PR già letta (nella stessa transazione).
// L'attore è chi ha fatto il push.
func (s *apiServer) emitPullAt(ctx context.Context, q querier, ia issueAccess, gs pullGitState, name string, p prRow, mod func(*domainevents.PullPayload)) error {
	return s.emitPull(ctx, q, name, ia, gs, p.Number, ia.userID, mod)
}

// pullConflictCheck confronta i branch: alla comparsa dei conflitti avvisa
// l'autore (pull_request.conflicts), quando spariscono azzera il segno.
func (s *apiServer) pullConflictCheck(ctx context.Context, ia issueAccess, g pullGit, p prRow) error {
	raw, err := g.caller.ReadJSON(ctx, ia.caller, ia.repo.ID, "mergeability", url.Values{"base": {p.TargetBranch}, "head": {p.SourceBranch}})
	if err != nil {
		var ae *gitclient.APIError
		if errors.As(err, &ae) || errors.Is(err, gitclient.ErrUnavailable) {
			slog.Default().Warn("conflitti della PR non letti", "repo", ia.repo.ID, "pull", p.Number, "err", err)
			return nil // un branch sparito o git giù: nessun avviso, il prossimo push ricalcola
		}
		return err
	}
	var mg struct {
		State     string   `json:"state"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &mg); err != nil {
		return err
	}
	conflicts := mg.State == "conflicts"
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var was bool
	var state string
	var draft bool
	var authorID uuid.UUID
	var title string
	if err := tx.QueryRow(ctx, `SELECT has_conflicts, state, draft, author_id, title FROM core.pull_requests WHERE id = $1 FOR UPDATE`, p.ID).
		Scan(&was, &state, &draft, &authorID, &title); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if state != "open" || was == conflicts {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET has_conflicts = $2 WHERE id = $1`, p.ID, conflicts); err != nil {
		return err
	}
	if conflicts {
		files := mg.Conflicts
		if len(files) > 10 {
			files = files[:10]
		}
		cur := p
		cur.Title, cur.Draft = title, draft
		if err := s.emitPullLight(ctx, tx, domainevents.PullRequestConflicts, repoSnapshot(ia.repo), cur, nil,
			func(pl *domainevents.PullPayload) { pl.Files = files }); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
