package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Nucleo delle Pull Request (v1.1, GIT-261): creazione fra due branch dello
// stesso repo (PR1), bozza e «Ready for review», modifica, chiusura e
// riapertura (PR6), elenco, dettaglio, commit, file e cronologia. Permessi
// come le issues (I3): chi legge vede, chi ha write apre e gestisce, l'autore
// gestisce la propria; un repo non leggibile è 404. Revisori, review,
// conversazioni, merge e stati dei commit sono degli item a valle.

func writePRBodyTooLarge(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Il testo supera 65536 caratteri.")
}

// canManagePull: l'autore e chi ha write (PR1, PR6).
func (ia issueAccess) canManagePull(p prRow) bool {
	return ia.write || p.AuthorID == ia.userID
}

func isUniqueOpenPair(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "pull_requests_open_branches_key"
}

// CreatePullRequest implementa POST /repos/{owner}/{repo}/pulls (PR1, PR6, I1).
func (s *apiServer) CreatePullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	// PR1: chi ha solo read propone con una issue.
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Aprire una Pull Request richiede il permesso write: con read puoi proporre la modifica aprendo una issue.")
		return
	}
	if !s.pullMutable(w, ctx, ia) {
		return
	}
	var in openapi.CreatePullRequestInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.SourceBranch == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "sourceBranch è obbligatorio.")
		return
	}
	title := strings.TrimSpace(in.Title)
	if msg := issueTitleError(title); msg != "" {
		writeFieldError(w, "title", msg)
		return
	}
	body := ""
	if in.Body != nil {
		body = *in.Body
	}
	if utf8.RuneCountInString(body) > maxIssueBodyRunes {
		writePRBodyTooLarge(w)
		return
	}
	if strings.ContainsRune(body, 0) || !utf8.ValidString(body) || strings.ContainsRune(title, 0) {
		writeFieldError(w, "body", "body: testo non valido.")
		return
	}
	draft := in.Draft != nil && *in.Draft
	source := in.SourceBranch
	target := ia.repo.DefaultBranch
	if in.TargetBranch != nil && *in.TargetBranch != "" {
		target = *in.TargetBranch
	}
	if source == target {
		writeFieldError(w, "targetBranch", "targetBranch: origine e destinazione sono lo stesso branch.")
		return
	}

	// Revisori (PR12): almeno read; per una bozza si ignorano.
	var reviewers []assignee
	if in.Reviewers != nil && !draft {
		seen := map[string]bool{}
		for _, n := range *in.Reviewers {
			key := strings.ToLower(n)
			if seen[key] {
				continue
			}
			seen[key] = true
			o, rerr := s.repoIdentity.ResolveOwner(ctx, key)
			if errors.Is(rerr, identityclient.ErrNotFound) || (rerr == nil && o.Type != "user") {
				writeFieldError(w, "reviewers", fmt.Sprintf("reviewers: %q non è un utente.", n))
				return
			}
			var can bool
			if rerr == nil {
				can, rerr = s.repoIdentity.HasRole(ctx, o.ID, ia.repo.ID, "read")
			}
			if rerr != nil {
				writeIssueFailure(w, "verifica dei revisori non riuscita", fmt.Errorf("%w: %v", errUsersUnavailable, rerr))
				return
			}
			if !can {
				writeFieldError(w, "reviewers", fmt.Sprintf("reviewers: %q non ha il permesso read sul repo.", n))
				return
			}
			if o.ID != ia.userID {
				reviewers = append(reviewers, assignee{id: o.ID, name: o.Name})
			}
		}
	}

	g, ok := s.pullGit(w, ia)
	if !ok {
		return
	}
	branches, err := g.branches(ctx)
	if err != nil {
		gitFailure(w, err)
		return
	}
	if _, ok := branches[source]; !ok {
		writeFieldError(w, "sourceBranch", fmt.Sprintf("sourceBranch: il branch %q non esiste.", source))
		return
	}
	if _, ok := branches[target]; !ok {
		writeFieldError(w, "targetBranch", fmt.Sprintf("targetBranch: il branch %q non esiste.", target))
		return
	}
	cmp, err := g.compare(ctx, target, source, false)
	if err != nil {
		gitFailure(w, err)
		return
	}
	if cmp.AheadBy == 0 {
		writeFieldError(w, "sourceBranch", fmt.Sprintf("sourceBranch: %q non ha commit di differenza rispetto a %q.", source, target))
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRepoForWrite(ctx, tx, ia.repo.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrArchived):
			writePullArchived(w)
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		default:
			writeIssueFailure(w, "verifica del repo non riuscita", err)
		}
		return
	}
	var existing int64
	err = tx.QueryRow(ctx, `SELECT number FROM core.pull_requests WHERE repo_id = $1 AND source_branch = $2
		AND target_branch = $3 AND state = 'open'`, ia.repo.ID, source, target).Scan(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "already_exists", fmt.Sprintf("Esiste già la Pull Request aperta #%d fra questi due branch.", existing))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeIssueFailure(w, "verifica delle Pull Request aperte non riuscita", err)
		return
	}

	// I1: il numero dal contatore del repo, nella stessa transazione
	// dell'INSERT; un rollback non consuma il numero.
	number, err := nextRepoNumber(ctx, tx, ia.repo.ID)
	if err != nil {
		writeIssueFailure(w, "assegnazione del numero non riuscita", err)
		return
	}
	prID := uuid.New()
	viaID, viaName := tokenOrigin(ia.caller)
	var base *string
	if cmp.MergeBase != "" {
		base = &cmp.MergeBase
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.pull_requests
		(id, repo_id, number, source_branch, target_branch, title, body, draft, author_id, head_sha, base_sha, via_token_id, via_token_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		prID, ia.repo.ID, number, source, target, title, body, draft, ia.userID, cmp.HeadSha, base, viaID, viaName); err != nil {
		if isUniqueOpenPair(err) {
			writeError(w, http.StatusConflict, "already_exists", "Esiste già una Pull Request aperta fra questi due branch.")
			return
		}
		writeIssueFailure(w, "creazione della Pull Request non riuscita", err)
		return
	}
	if err := insertPullEvent(ctx, tx, prID, "opened", ia.userID, map[string]any{"draft": draft, "sourceBranch": source, "targetBranch": target}); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	for _, rv := range reviewers {
		if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_review_requests (pull_request_id, reviewer_id, requested_by)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, prID, rv.id, ia.userID); err != nil {
			writeIssueFailure(w, "richiesta dei revisori non riuscita", err)
			return
		}
	}
	created, err := getPR(ctx, tx, ia.repo.ID, number, false)
	if err == nil {
		err = s.syncPullClosing(ctx, tx, ia, created, body)
	}
	gs := pullGitState{srcExists: true, haveCmp: true, live: true, cmp: cmp}
	if err == nil {
		err = s.emitPull(ctx, tx, domainevents.PullRequestOpened, ia, gs, number, ia.userID, nil)
	}
	if err == nil {
		err = s.emitReviewRequested(ctx, tx, domainevents.PullRequestReviewRequested, ia, gs, number, reviewers)
	}
	if err != nil {
		writeIssueFailure(w, "scrittura degli eventi non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della Pull Request non riuscito", err)
		return
	}
	s.respondPull(w, ctx, http.StatusCreated, ia, number, map[string]string{"Location": pullLocation(ia, number)})
}

// GetPullRequest implementa GET /repos/{owner}/{repo}/pulls/{number}.
func (s *apiServer) GetPullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if _, ok := s.pullFor(w, r.Context(), s.pool, ia, number, false); !ok {
		return
	}
	s.respondPull(w, r.Context(), http.StatusOK, ia, number, nil)
}

// UpdatePullRequest implementa PATCH .../pulls/{number}: titolo, testo e
// branch di destinazione. Possono l'autore e chi ha write.
func (s *apiServer) UpdatePullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	if !ia.canManagePull(cur) {
		writeError(w, http.StatusForbidden, "forbidden", "Modifica la Pull Request chi ha write o l'autore.")
		return
	}
	var in openapi.UpdatePullRequestInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Title == nil && in.Body == nil && in.TargetBranch == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Serve almeno title, body o targetBranch.")
		return
	}
	var newTitle string
	if in.Title != nil {
		newTitle = strings.TrimSpace(*in.Title)
		if msg := issueTitleError(newTitle); msg != "" {
			writeFieldError(w, "title", msg)
			return
		}
	}
	if in.Body != nil {
		if utf8.RuneCountInString(*in.Body) > maxIssueBodyRunes {
			writePRBodyTooLarge(w)
			return
		}
		if strings.ContainsRune(*in.Body, 0) || !utf8.ValidString(*in.Body) {
			writeFieldError(w, "body", "body: testo non valido.")
			return
		}
	}
	if cur.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "Solo una Pull Request aperta si modifica.")
		return
	}
	retarget := in.TargetBranch != nil && *in.TargetBranch != cur.TargetBranch
	var newBase *string
	if retarget {
		if *in.TargetBranch == cur.SourceBranch {
			writeFieldError(w, "targetBranch", "targetBranch: origine e destinazione sono lo stesso branch.")
			return
		}
		g, ok := s.pullGit(w, ia)
		if !ok {
			return
		}
		branches, err := g.branches(ctx)
		if err != nil {
			gitFailure(w, err)
			return
		}
		_, hasDst := branches[*in.TargetBranch]
		_, hasSrc := branches[cur.SourceBranch]
		if !hasDst {
			writeFieldError(w, "targetBranch", fmt.Sprintf("targetBranch: il branch %q non esiste.", *in.TargetBranch))
			return
		}
		if hasSrc {
			cmp, err := g.compare(ctx, *in.TargetBranch, cur.SourceBranch, false)
			if err != nil {
				gitFailure(w, err)
				return
			}
			if cmp.MergeBase != "" {
				newBase = &cmp.MergeBase
			}
		}
	}

	view := cur
	if retarget {
		view.TargetBranch = *in.TargetBranch
	}
	gs := s.pullGitBestEffort(ctx, ia, view)
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "Solo una Pull Request aperta si modifica.")
		return
	}
	titleChanged := in.Title != nil && newTitle != p.Title
	bodyChanged := in.Body != nil && *in.Body != p.Body
	if titleChanged || bodyChanged || retarget {
		title, body, target, base := p.Title, p.Body, p.TargetBranch, p.BaseSha
		if titleChanged {
			title = newTitle
		}
		if bodyChanged {
			body = *in.Body
		}
		if retarget {
			target = *in.TargetBranch
			if newBase != nil {
				base = newBase
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET title = $2, body = $3, target_branch = $4, base_sha = $5, updated_at = now()
			WHERE id = $1`, p.ID, title, body, target, base); err != nil {
			if isUniqueOpenPair(err) {
				writeError(w, http.StatusConflict, "already_exists", "Esiste già una Pull Request aperta fra questi due branch.")
				return
			}
			writeIssueFailure(w, "modifica della Pull Request non riuscita", err)
			return
		}
		if titleChanged {
			if err := insertPullEvent(ctx, tx, p.ID, "renamed", ia.userID, map[string]any{"from": p.Title, "to": newTitle}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
		if bodyChanged {
			if err := insertPullEvent(ctx, tx, p.ID, "edited", ia.userID, nil); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
		if retarget {
			if err := insertPullEvent(ctx, tx, p.ID, "retargeted", ia.userID, map[string]any{"from": p.TargetBranch, "to": target}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
	}
	if titleChanged || bodyChanged || retarget {
		upd, err := getPR(ctx, tx, ia.repo.ID, number, false)
		if err == nil && bodyChanged {
			err = s.syncPullClosing(ctx, tx, ia, upd, *in.Body)
		}
		if err == nil {
			err = s.emitPull(ctx, tx, domainevents.PullRequestEdited, ia, gs, number, ia.userID, func(pl *domainevents.PullPayload) {
				ch := &domainevents.IssueChanges{}
				if titleChanged {
					ch.Title = &domainevents.From{From: p.Title}
				}
				if bodyChanged {
					ch.Body = &domainevents.From{From: p.Body}
				}
				if retarget {
					pl.Reason = "retargeted"
				}
				pl.Changes = ch
			})
		}
		if err != nil {
			writeIssueFailure(w, "scrittura degli eventi non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondPull(w, ctx, http.StatusOK, ia, number, nil)
}

// pullTransition esegue una transizione di stato (bozza, pronta, chiusa,
// riaperta) con le sue regole. check ritorna (codice, messaggio) del 409 se la
// transizione non è ammessa; apply scrive la modifica e l'evento.
func (s *apiServer) pullTransition(w http.ResponseWriter, r *http.Request, owner, name string, number int64, event string,
	pre func(p prRow) (string, string),
	prepare func(ia issueAccess, p prRow) (extra any, ok bool),
	apply func(tx pgx.Tx, ia issueAccess, p prRow, extra any) error) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	if !ia.canManagePull(cur) {
		writeError(w, http.StatusForbidden, "forbidden", "Gestisce la Pull Request chi ha write o l'autore.")
		return
	}
	if code, msg := pre(cur); code != "" {
		writeError(w, http.StatusConflict, code, msg)
		return
	}
	view := cur
	if event == domainevents.PullRequestReopened {
		view.State = "open" // si legge il confronto fra i branch vivi
	}
	gs := s.pullGitBestEffort(ctx, ia, view)
	var extra any
	if prepare != nil {
		if extra, ok = prepare(ia, cur); !ok {
			return
		}
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if code, msg := pre(p); code != "" {
		writeError(w, http.StatusConflict, code, msg)
		return
	}
	if err := apply(tx, ia, p, extra); err != nil {
		if isUniqueOpenPair(err) {
			writeError(w, http.StatusConflict, "already_exists", "Esiste già una Pull Request aperta fra questi due branch.")
			return
		}
		writeIssueFailure(w, "modifica della Pull Request non riuscita", err)
		return
	}
	if err := s.emitPull(ctx, tx, event, ia, gs, number, ia.userID, nil); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondPull(w, ctx, http.StatusOK, ia, number, nil)
}

func notOpen(p prRow) (string, string) {
	if p.State == "merged" {
		return "already_merged", "La Pull Request è già stata unita: è definitiva."
	}
	if p.State != "open" {
		return "not_open", "La Pull Request non è aperta."
	}
	return "", ""
}

// MarkPullRequestReady implementa POST .../ready: bozza → Ready for review.
func (s *apiServer) MarkPullRequestReady(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	s.pullTransition(w, r, owner, name, number, domainevents.PullRequestReadyForReview,
		func(p prRow) (string, string) {
			if c, m := notOpen(p); c != "" {
				return c, m
			}
			if !p.Draft {
				return "not_draft", "La Pull Request non è una bozza."
			}
			return "", ""
		}, nil,
		func(tx pgx.Tx, ia issueAccess, p prRow, _ any) error {
			if _, err := tx.Exec(r.Context(), `UPDATE core.pull_requests SET draft = false, updated_at = now() WHERE id = $1`, p.ID); err != nil {
				return err
			}
			return insertPullEvent(r.Context(), tx, p.ID, "ready_for_review", ia.userID, nil)
		})
}

// MarkPullRequestDraft implementa POST .../draft: torna una bozza.
func (s *apiServer) MarkPullRequestDraft(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	s.pullTransition(w, r, owner, name, number, domainevents.PullRequestConvertedToDraft,
		func(p prRow) (string, string) {
			if c, m := notOpen(p); c != "" {
				return c, m
			}
			if p.Draft {
				return "already_draft", "La Pull Request è già una bozza."
			}
			return "", ""
		}, nil,
		func(tx pgx.Tx, ia issueAccess, p prRow, _ any) error {
			if _, err := tx.Exec(r.Context(), `UPDATE core.pull_requests SET draft = true, updated_at = now() WHERE id = $1`, p.ID); err != nil {
				return err
			}
			return insertPullEvent(r.Context(), tx, p.ID, "converted_to_draft", ia.userID, nil)
		})
}

// ClosePullRequest implementa POST .../close: chiude senza unire e non tocca
// i branch (PR6).
func (s *apiServer) ClosePullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	s.pullTransition(w, r, owner, name, number, domainevents.PullRequestClosed,
		func(p prRow) (string, string) {
			switch p.State {
			case "merged":
				return "already_merged", "La Pull Request è già stata unita."
			case "closed":
				return "already_closed", "La Pull Request è già chiusa."
			}
			return "", ""
		}, nil,
		func(tx pgx.Tx, ia issueAccess, p prRow, _ any) error {
			// La bozza si azzera: draft è sempre false se non è aperta.
			if _, err := tx.Exec(r.Context(), `UPDATE core.pull_requests SET state = 'closed', draft = false,
				closed_at = now(), updated_at = now() WHERE id = $1`, p.ID); err != nil {
				return err
			}
			return insertPullEvent(r.Context(), tx, p.ID, "closed", ia.userID, nil)
		})
}

// reopenState è ciò che la riapertura rilegge da git prima della transazione.
type reopenState struct {
	head string
	base *string
}

// ReopenPullRequest implementa POST .../reopen: solo se i due branch
// esistono ancora (PR6); una PR unita è definitiva.
func (s *apiServer) ReopenPullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	s.pullTransition(w, r, owner, name, number, domainevents.PullRequestReopened,
		func(p prRow) (string, string) {
			switch p.State {
			case "merged":
				return "already_merged", "La Pull Request è già stata unita: è definitiva."
			case "open":
				return "already_open", "La Pull Request è già aperta."
			}
			return "", ""
		},
		func(ia issueAccess, p prRow) (any, bool) {
			g, ok := s.pullGit(w, ia)
			if !ok {
				return nil, false
			}
			branches, err := g.branches(r.Context())
			if err != nil {
				gitFailure(w, err)
				return nil, false
			}
			_, hasSrc := branches[p.SourceBranch]
			_, hasDst := branches[p.TargetBranch]
			if !hasSrc || !hasDst {
				missing := p.SourceBranch
				if hasSrc {
					missing = p.TargetBranch
				}
				writeError(w, http.StatusConflict, "branch_missing", fmt.Sprintf("Il branch %q non esiste più: la Pull Request non si riapre.", missing))
				return nil, false
			}
			cmp, err := g.compare(r.Context(), p.TargetBranch, p.SourceBranch, false)
			if err != nil {
				gitFailure(w, err)
				return nil, false
			}
			st := reopenState{head: cmp.HeadSha}
			if cmp.MergeBase != "" {
				st.base = &cmp.MergeBase
			}
			return st, true
		},
		func(tx pgx.Tx, ia issueAccess, p prRow, extra any) error {
			st := extra.(reopenState)
			if _, err := tx.Exec(r.Context(), `UPDATE core.pull_requests SET state = 'open', closed_at = NULL,
				head_sha = $2, base_sha = $3, updated_at = now() WHERE id = $1`, p.ID, st.head, st.base); err != nil {
				return err
			}
			return insertPullEvent(r.Context(), tx, p.ID, "reopened", ia.userID, nil)
		})
}
