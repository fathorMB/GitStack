package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Merge delle Pull Request (v1.1, GIT-263): impostazioni «Branches & merge»
// (PR3), stato di unibilità con conflitti e istruzioni (PR7), merge commit o
// squash, eliminazione del branch di origine a mano o in automatico (PR3).
// Il merge vero lo fa il servizio git (GIT-260); core decide se si può e
// registra l'esito. Il gate di approvazioni e stati obbligatori (PR4, PR9) è
// di GIT-264 e si aggancia a mergeGateBlockers.

const maxMergeMessageRunes = 65536

// branchSettings sono le impostazioni «Branches & merge» di un repo; senza
// riga valgono i default (entrambi i metodi, squash proposto, nessuna PR
// obbligatoria, il branch non si elimina da solo).
type branchSettings struct {
	methods       []string
	defaultMethod string
	deleteOnMerge bool
	requirePR     bool
	approvals     int
	contexts      []string
}

func defaultBranchSettings() branchSettings {
	return branchSettings{methods: []string{"merge", "squash"}, defaultMethod: "squash", approvals: 1, contexts: []string{}}
}

func loadBranchSettings(ctx context.Context, q querier, repoID uuid.UUID) (branchSettings, error) {
	bs := defaultBranchSettings()
	var approvals int16
	err := q.QueryRow(ctx, `SELECT merge_methods, default_merge_method, delete_branch_on_merge, require_pull_request,
		required_approvals, required_contexts FROM core.branch_settings WHERE repo_id = $1`, repoID).
		Scan(&bs.methods, &bs.defaultMethod, &bs.deleteOnMerge, &bs.requirePR, &approvals, &bs.contexts)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultBranchSettings(), nil
	}
	if err != nil {
		return bs, err
	}
	bs.approvals = int(approvals)
	if bs.contexts == nil {
		bs.contexts = []string{}
	}
	return bs, nil
}

func (b branchSettings) allows(method string) bool {
	for _, m := range b.methods {
		if m == method {
			return true
		}
	}
	return false
}

func (b branchSettings) api() openapi.BranchSettings {
	dismiss := true
	return openapi.BranchSettings{
		AllowMergeCommit: b.allows("merge"), AllowSquashMerge: b.allows("squash"),
		DefaultMergeMethod: openapi.MergeMethod(b.defaultMethod), DeleteBranchOnMerge: b.deleteOnMerge,
		RequirePullRequest: b.requirePR, RequiredApprovals: b.approvals,
		RequiredStatusContexts: b.contexts, DismissStaleApprovals: &dismiss,
	}
}

// GetBranchSettings implementa GET /repos/{owner}/{repo}/branch-settings.
func (s *apiServer) GetBranchSettings(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	bs, err := loadBranchSettings(r.Context(), s.pool, ia.repo.ID)
	if err != nil {
		writeIssueFailure(w, "lettura delle impostazioni non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, bs.api())
}

// UpdateBranchSettings implementa PATCH …/branch-settings (solo admin).
func (s *apiServer) UpdateBranchSettings(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Le impostazioni dei branch e del merge le modifica chi ha il ruolo admin.")
		return
	}
	var in openapi.UpdateBranchSettingsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.AllowMergeCommit == nil && in.AllowSquashMerge == nil && in.DefaultMergeMethod == nil && in.DeleteBranchOnMerge == nil &&
		in.RequirePullRequest == nil && in.RequiredApprovals == nil && in.RequiredStatusContexts == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Serve almeno un campo da modificare.")
		return
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRepoForWrite(ctx, tx, ia.repo.ID); err != nil {
		writeRepoLockFailure(w, err)
		return
	}
	// Serializza due modifiche concorrenti delle impostazioni dello stesso repo.
	if _, err := tx.Exec(ctx, `INSERT INTO core.branch_settings (repo_id) VALUES ($1) ON CONFLICT DO NOTHING`, ia.repo.ID); err != nil {
		writeIssueFailure(w, "scrittura delle impostazioni non riuscita", err)
		return
	}
	var cur branchSettings
	var approvals int16
	if err := tx.QueryRow(ctx, `SELECT merge_methods, default_merge_method, delete_branch_on_merge, require_pull_request,
		required_approvals, required_contexts FROM core.branch_settings WHERE repo_id = $1 FOR UPDATE`, ia.repo.ID).
		Scan(&cur.methods, &cur.defaultMethod, &cur.deleteOnMerge, &cur.requirePR, &approvals, &cur.contexts); err != nil {
		writeIssueFailure(w, "lettura delle impostazioni non riuscita", err)
		return
	}
	cur.approvals = int(approvals)

	allowMerge, allowSquash := cur.allows("merge"), cur.allows("squash")
	if in.AllowMergeCommit != nil {
		allowMerge = *in.AllowMergeCommit
	}
	if in.AllowSquashMerge != nil {
		allowSquash = *in.AllowSquashMerge
	}
	var methods []string
	if allowMerge {
		methods = append(methods, "merge")
	}
	if allowSquash {
		methods = append(methods, "squash")
	}
	if len(methods) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no_merge_method", "Almeno un metodo di merge deve restare ammesso.")
		return
	}
	next := cur
	next.methods = methods
	if in.DefaultMergeMethod != nil {
		next.defaultMethod = string(*in.DefaultMergeMethod)
		if !next.allows(next.defaultMethod) {
			writeError(w, http.StatusUnprocessableEntity, "default_method_not_allowed", "defaultMergeMethod deve essere un metodo ammesso.")
			return
		}
	} else if !next.allows(next.defaultMethod) {
		// Il metodo proposto non è più ammesso: si propone l'unico rimasto.
		next.defaultMethod = methods[0]
	}
	if in.DeleteBranchOnMerge != nil {
		next.deleteOnMerge = *in.DeleteBranchOnMerge
	}
	if in.RequirePullRequest != nil {
		next.requirePR = *in.RequirePullRequest
	}
	if in.RequiredApprovals != nil {
		if *in.RequiredApprovals < 1 || *in.RequiredApprovals > 5 {
			writeFieldError(w, "requiredApprovals", "requiredApprovals: da 1 a 5.")
			return
		}
		next.approvals = *in.RequiredApprovals
	}
	if in.RequiredStatusContexts != nil {
		seen := map[string]bool{}
		ctxs := []string{}
		for _, c := range *in.RequiredStatusContexts {
			c = strings.TrimSpace(c)
			if c == "" || utf8.RuneCountInString(c) > 255 || strings.ContainsRune(c, 0) || !utf8.ValidString(c) {
				writeFieldError(w, "requiredStatusContexts", "requiredStatusContexts: contesti da 1 a 255 caratteri.")
				return
			}
			if !seen[c] {
				seen[c] = true
				ctxs = append(ctxs, c)
			}
		}
		if len(ctxs) > 20 {
			writeFieldError(w, "requiredStatusContexts", "requiredStatusContexts: al massimo 20 contesti.")
			return
		}
		next.contexts = ctxs
	}
	if _, err := tx.Exec(ctx, `UPDATE core.branch_settings SET merge_methods = $2, default_merge_method = $3,
		delete_branch_on_merge = $4, require_pull_request = $5, required_approvals = $6, required_contexts = $7,
		updated_by = $8, updated_at = now() WHERE repo_id = $1`, ia.repo.ID, next.methods, next.defaultMethod,
		next.deleteOnMerge, next.requirePR, next.approvals, next.contexts, ia.userID); err != nil {
		writeIssueFailure(w, "scrittura delle impostazioni non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit delle impostazioni non riuscito", err)
		return
	}
	writeJSON(w, http.StatusOK, next.api())
}

func writeRepoLockFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrArchived):
		writePullArchived(w)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
	default:
		writeIssueFailure(w, "verifica del repo non riuscita", err)
	}
}

// conflictInstructions sono i comandi per risolvere i conflitti in locale (PR7).
func conflictInstructions(number int64, target string) []string {
	return []string{
		fmt.Sprintf("gs pr checkout %d", number),
		"git merge origin/" + target,
		"# risolvi i conflitti, poi",
		"git commit && git push",
	}
}

const maxConflictFiles = 100

// mergeEval è l'esito del calcolo di unibilità: la risposta dell'API più i dati
// che servono al merge (punta attuale della destinazione).
type mergeEval struct {
	status   openapi.PullRequestMergeability
	settings branchSettings
	baseSha  string
	conflict []string
}

func (e *mergeEval) block(code openapi.MergeBlockerCode, msg string) {
	e.status.Blockers = append(e.status.Blockers, openapi.MergeBlocker{Code: code, Message: msg})
}

func (e *mergeEval) blocked(code openapi.MergeBlockerCode) bool {
	for _, b := range e.status.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

// mergeGateBlockers è il punto unico di controllo pre-merge: ritorna i motivi
// per cui la PR (aperta, senza conflitti) non si può unire. Qui c'è la bozza
// (PR6); GIT-264 aggiunge approvazioni e modifiche richieste (PR4) e stati
// obbligatori (PR9) in questa funzione, e la usano sia la mergeability sia il
// merge.
func (s *apiServer) mergeGateBlockers(ctx context.Context, q querier, ia issueAccess, p prRow, ev *mergeEval) ([]openapi.MergeBlocker, error) {
	var out []openapi.MergeBlocker
	if p.Draft {
		out = append(out, openapi.MergeBlocker{Code: openapi.MergeBlockerCodeDraft,
			Message: "La Pull Request è una bozza: segnala «Ready for review» prima di unirla."})
	}
	// PR4: con «Richiedi una Pull Request» servono le approvazioni di persone
	// con write sull'ultimo commit (un nuovo commit annulla le precedenti, perché
	// pullReviewState conta solo quelle sullo sha attuale) e nessuna richiesta
	// di modifiche aperta.
	if ev.settings.requirePR {
		decision, approvals, required, err := pullReviewState(ctx, q, ia.repo.ID, p, ev.status.HeadSha, 0)
		if err != nil {
			return nil, err
		}
		if decision == openapi.ReviewDecision("changes_requested") {
			out = append(out, openapi.MergeBlocker{Code: openapi.MergeBlockerCodeChangesRequested,
				Message: "Un revisore ha chiesto modifiche: serve una nuova approvazione."})
		}
		if approvals < required {
			out = append(out, openapi.MergeBlocker{Code: openapi.MergeBlockerCodeApprovalsRequired,
				Message: fmt.Sprintf("Servono %d approvazioni di persone con permesso write sull'ultimo commit (%d ricevute).", required, approvals)})
		}
	}
	// PR9: i contesti obbligatori vogliono un success sullo sha attuale.
	if len(ev.settings.contexts) > 0 {
		byCtx, err := commitStatusStates(ctx, q, ia.repo.ID, ev.status.HeadSha)
		if err != nil {
			return nil, err
		}
		out = append(out, statusGateBlockers(ev.settings.contexts, byCtx)...)
	}
	return out, nil
}

// evaluateMerge calcola lo stato di unibilità sui commit attuali.
func (s *apiServer) evaluateMerge(ctx context.Context, q querier, ia issueAccess, g pullGit, p prRow) (*mergeEval, error) {
	bs, err := loadBranchSettings(ctx, q, ia.repo.ID)
	if err != nil {
		return nil, err
	}
	ev := &mergeEval{settings: bs}
	methods := []openapi.MergeMethod{}
	for _, m := range bs.methods {
		methods = append(methods, openapi.MergeMethod(m))
	}
	ev.status = openapi.PullRequestMergeability{
		ConflictFiles: []string{}, Blockers: []openapi.MergeBlocker{}, AllowedMethods: methods,
		DefaultMethod: openapi.MergeMethod(bs.defaultMethod), HeadSha: sha40(p.HeadSha),
		State: openapi.PullRequestMergeabilityStateBlocked,
	}
	_, ev.status.Approvals, ev.status.RequiredApprovals, err = pullReviewState(ctx, q, ia.repo.ID, p, ev.status.HeadSha, 0)
	if err != nil {
		return nil, err
	}
	if p.State != "open" {
		ev.block(openapi.MergeBlockerCodeNotOpen, "La Pull Request non è aperta.")
		return ev, nil
	}
	branches, err := g.branches(ctx)
	if err != nil {
		return nil, err
	}
	for _, br := range []string{p.SourceBranch, p.TargetBranch} {
		if _, ok := branches[br]; !ok {
			ev.block(openapi.MergeBlockerCodeNotOpen, fmt.Sprintf("Il branch %q non esiste più.", br))
			return ev, nil
		}
	}
	ev.baseSha = branches[p.TargetBranch]
	cmp, err := g.compare(ctx, p.TargetBranch, p.SourceBranch, false)
	if err != nil {
		return nil, err
	}
	ev.status.AheadBy, ev.status.BehindBy, ev.status.HeadSha = cmp.AheadBy, cmp.BehindBy, cmp.HeadSha
	// Le approvazioni valgono sull'ultimo commit: dopo un nuovo commit non contano.
	_, ev.status.Approvals, ev.status.RequiredApprovals, err = pullReviewState(ctx, q, ia.repo.ID, p, ev.status.HeadSha, 0)
	if err != nil {
		return nil, err
	}
	raw, err := g.caller.ReadJSON(ctx, g.ia.caller, g.ia.repo.ID, "mergeability",
		url.Values{"base": {p.TargetBranch}, "head": {p.SourceBranch}})
	if err != nil {
		return nil, err
	}
	var mg struct {
		State     string   `json:"state"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &mg); err != nil {
		return nil, err
	}
	switch mg.State {
	case "conflicts":
		ev.conflict = mg.Conflicts
		files := mg.Conflicts
		if len(files) > maxConflictFiles {
			files = files[:maxConflictFiles]
			t := true
			ev.status.ConflictFilesTruncated = &t
		}
		ev.status.ConflictFiles = append([]string{}, files...)
		ins := conflictInstructions(p.Number, p.TargetBranch)
		ev.status.ConflictInstructions = &ins
		ev.block(openapi.MergeBlockerCodeConflicts, "This branch has conflicts that must be resolved.")
	case "empty":
		ev.block(openapi.MergeBlockerCodeNothingToMerge, "Il branch di origine è già contenuto nella destinazione: non c'è niente da unire.")
	case "unrelated":
		ev.block(openapi.MergeBlockerCodeUnrelatedHistories, "I due branch non hanno storia in comune.")
	}
	gate, err := s.mergeGateBlockers(ctx, q, ia, p, ev)
	if err != nil {
		return nil, err
	}
	ev.status.Blockers = append(ev.status.Blockers, gate...)
	switch {
	case ev.blocked(openapi.MergeBlockerCodeConflicts):
		ev.status.State = openapi.PullRequestMergeabilityStateConflicts
	case len(ev.status.Blockers) > 0:
		ev.status.State = openapi.PullRequestMergeabilityStateBlocked
	default:
		ev.status.State = openapi.PullRequestMergeabilityStateMergeable
		ev.status.Mergeable = true
	}
	return ev, nil
}

// GetPullRequestMergeability implementa GET …/pulls/{number}/mergeability.
func (s *apiServer) GetPullRequestMergeability(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	p, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok {
		return
	}
	g, ok := s.pullGit(w, ia)
	if !ok {
		return
	}
	ev, err := s.evaluateMerge(ctx, s.pool, ia, g, p)
	if err != nil {
		s.writePullBuildFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ev.status)
}

// blockerStatus: il codice HTTP e quello d'errore di un blocco al merge.
func writeMergeBlocked(w http.ResponseWriter, p prRow, ev *mergeEval) {
	b := ev.status.Blockers[0]
	for _, c := range ev.status.Blockers {
		if c.Code == openapi.MergeBlockerCodeNotOpen {
			b = c
			break
		}
	}
	details := map[string]any{"blockers": ev.status.Blockers}
	if b.Code == openapi.MergeBlockerCodeConflicts {
		details["files"] = ev.status.ConflictFiles
		details["instructions"] = conflictInstructions(p.Number, p.TargetBranch)
	}
	writeErrorDetails(w, http.StatusConflict, string(b.Code), b.Message, details)
}

// mergeActor è chi unisce: nome ed email per il commit e tipo per l'evento push.
func (s *apiServer) mergeActor(ctx context.Context, ia issueAccess) (gitclient.Identity, gitclient.Pusher) {
	username, kind, email := ia.caller.Username, "human", ""
	if look, ok := s.repoIdentity.(identityclient.UserLookup); ok {
		if found, err := look.LookupUsers(ctx, []uuid.UUID{ia.userID}); err == nil {
			if u, ok := found[ia.userID]; ok {
				if u.Username != "" {
					username = u.Username
				}
				if u.Kind != "" {
					kind = u.Kind
				}
				email = u.Email
			}
		}
	}
	if email == "" {
		email = username + "@users.noreply.gitstack"
	}
	return gitclient.Identity{Name: username, Email: email},
		gitclient.Pusher{ID: ia.userID.String(), Username: username, Type: kind}
}

func (s *apiServer) pullMerger(w http.ResponseWriter) (gitclient.Merger, bool) {
	m, ok := s.git.(gitclient.Merger)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Merge non disponibile.")
		return nil, false
	}
	return m, true
}

// writeMergeGitError traduce il rifiuto di git su un merge.
func writeMergeGitError(w http.ResponseWriter, p prRow, err error) {
	var me *gitclient.MergeError
	if !errors.As(err, &me) {
		gitFailure(w, err)
		return
	}
	switch me.Code {
	case "conflicts":
		files := me.Files
		if files == nil {
			files = []string{}
		}
		writeErrorDetails(w, http.StatusConflict, "conflicts", "This branch has conflicts that must be resolved.",
			map[string]any{"files": files, "instructions": conflictInstructions(p.Number, p.TargetBranch)})
	case "stale":
		if me.Ref == p.SourceBranch {
			writeError(w, http.StatusConflict, "head_changed", "Il branch di origine è cambiato durante il merge: ricarica la Pull Request e riprova.")
			return
		}
		writeError(w, http.StatusConflict, "base_changed",
			fmt.Sprintf("Il branch %q è cambiato durante il merge (un'altra unione è arrivata prima): ricarica la Pull Request e riprova.", p.TargetBranch))
	case "nothing_to_merge":
		writeError(w, http.StatusConflict, "nothing_to_merge", "Il branch di origine è già contenuto nella destinazione: non c'è niente da unire.")
	case "unrelated_histories":
		writeError(w, http.StatusConflict, "unrelated_histories", "I due branch non hanno storia in comune.")
	default:
		if me.Status == http.StatusNotFound || me.Status == http.StatusBadRequest {
			msg := me.Message
			if msg == "" {
				msg = "Richiesta non soddisfatta."
			}
			writeError(w, me.Status, me.Code, msg)
			return
		}
		writeError(w, http.StatusConflict, me.Code, "Il servizio git ha rifiutato il merge.")
	}
}

// MergePullRequest implementa POST …/pulls/{number}/merge.
func (s *apiServer) MergePullRequest(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	// PR3: unisce chi ha write, agenti compresi.
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Unire una Pull Request richiede il permesso write.")
		return
	}
	if _, ok := s.pullFor(w, ctx, s.pool, ia, number, false); !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	var in openapi.MergePullRequestInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	method := string(in.Method)
	if method != "merge" && method != "squash" {
		writeFieldError(w, "method", "method: merge o squash.")
		return
	}
	var title, msg string
	if in.CommitTitle != nil {
		title = strings.TrimSpace(*in.CommitTitle)
	}
	if in.CommitMessage != nil {
		msg = *in.CommitMessage
	}
	for _, v := range []string{title, msg} {
		if utf8.RuneCountInString(v) > maxMergeMessageRunes || strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
			writeFieldError(w, "commitMessage", "commitTitle/commitMessage: testo non valido o troppo lungo.")
			return
		}
	}
	if strings.ContainsAny(title, "\r\n") {
		writeFieldError(w, "commitTitle", "commitTitle: una sola riga.")
		return
	}
	g, ok := s.pullGit(w, ia)
	if !ok {
		return
	}
	merger, ok := s.pullMerger(w)
	if !ok {
		return
	}
	author, pusher := s.mergeActor(ctx, ia)

	// Il lock sulla riga della PR serializza due merge della stessa PR: il
	// secondo, dopo il primo, la trova unita (409 already_merged). Due PR sulla
	// stessa base si decidono in git, con lo sha atteso della destinazione.
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.State == "merged" {
		writeError(w, http.StatusConflict, "already_merged", "La Pull Request è già stata unita.")
		return
	}
	ev, err := s.evaluateMerge(ctx, tx, ia, g, p)
	if err != nil {
		s.writePullBuildFailure(w, err)
		return
	}
	if !ev.settings.allows(method) {
		writeError(w, http.StatusConflict, "method_not_allowed", fmt.Sprintf("Il metodo %q non è ammesso da questo repo.", method))
		return
	}
	if len(ev.status.Blockers) > 0 {
		writeMergeBlocked(w, p, ev)
		return
	}
	if in.ExpectedHeadSha != nil && *in.ExpectedHeadSha != ev.status.HeadSha {
		writeError(w, http.StatusConflict, "head_changed", "Il branch di origine è cambiato: ricarica la Pull Request e riprova.")
		return
	}

	if title == "" {
		if method == "squash" {
			title = fmt.Sprintf("%s (#%d)", p.Title, p.Number)
		} else {
			title = fmt.Sprintf("Merge pull request #%d from %s", p.Number, p.SourceBranch)
		}
	}
	full := title
	switch {
	case msg != "":
		full += "\n\n" + msg
	case method == "squash" && p.Body != "":
		full += "\n\n" + p.Body
	case method == "merge":
		full += "\n\n" + p.Title
	}
	res, err := merger.Merge(ctx, ia.caller, ia.repo.ID, gitclient.MergeRequest{
		Base: p.TargetBranch, Head: p.SourceBranch, Method: method, Message: full,
		ExpectedBaseSha: ev.baseSha, ExpectedHeadSha: ev.status.HeadSha, Author: author,
		Owner: ia.repo.OwnerName, Name: ia.repo.Name, DefaultBranch: ia.repo.DefaultBranch, Pusher: pusher,
	})
	if err != nil {
		writeMergeGitError(w, p, err)
		return
	}
	var base *string
	if res.MergeBase != "" {
		base = &res.MergeBase
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET state = 'merged', draft = false, merge_method = $2,
		merge_commit_sha = $3, merged_by = $4, merged_at = now(), head_sha = $5, base_sha = $6, updated_at = now()
		WHERE id = $1`, p.ID, method, res.Sha, ia.userID, res.HeadSha, base); err != nil {
		// Il merge in git è già fatto: non si può annullare. Lo si dice nel log.
		slog.Default().Error("merge eseguito in git ma non registrato", "repo", ia.repo.ID, "pull", p.Number, "sha", res.Sha, "err", err)
		writeIssueFailure(w, "registrazione del merge non riuscita", err)
		return
	}
	if err := insertPullEvent(ctx, tx, p.ID, "merged", ia.userID, map[string]any{
		"method": method, "sha": res.Sha, "sourceBranch": p.SourceBranch, "targetBranch": p.TargetBranch}); err != nil {
		slog.Default().Error("merge eseguito in git ma evento non scritto", "repo", ia.repo.ID, "pull", p.Number, "err", err)
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Default().Error("merge eseguito in git ma commit non riuscito", "repo", ia.repo.ID, "pull", p.Number, "sha", res.Sha, "err", err)
		writeIssueFailure(w, "commit del merge non riuscito", err)
		return
	}
	// Eliminazione del branch (PR3): su richiesta o per impostazione del repo.
	// Il merge è già definitivo: se non riesce resta il bottone «Delete branch».
	if (in.DeleteBranch != nil && *in.DeleteBranch) || ev.settings.deleteOnMerge {
		merged, err := getPR(ctx, s.pool, ia.repo.ID, number, false)
		if err == nil {
			if f := s.deleteMergedBranch(ctx, ia, merger, merged); f != nil {
				slog.Default().Info("branch non eliminato dopo il merge", "repo", ia.repo.ID, "pull", number, "code", f.code)
			}
		}
	}
	s.respondPull(w, ctx, http.StatusOK, ia, number, nil)
}

// branchFailure è il motivo per cui il branch non si elimina.
type branchFailure struct {
	status  int
	code    string
	message string
}

func (f *branchFailure) write(w http.ResponseWriter) {
	writeError(w, f.status, f.code, f.message)
}

// deleteMergedBranch elimina il branch di origine di una PR unita. Mai il
// branch principale, né uno usato da altre PR aperte, né uno che dopo il
// merge ha ricevuto altri commit (si perderebbero). nil se eliminato.
func (s *apiServer) deleteMergedBranch(ctx context.Context, ia issueAccess, merger gitclient.Merger, p prRow) *branchFailure {
	if p.State != "merged" {
		return &branchFailure{http.StatusConflict, "not_merged", "Il branch si elimina solo dopo il merge della Pull Request."}
	}
	if p.SourceBranch == ia.repo.DefaultBranch {
		return &branchFailure{http.StatusConflict, "branch_in_use", "Il branch principale non si elimina."}
	}
	var others int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_requests WHERE repo_id = $1 AND state = 'open'
		AND (source_branch = $2 OR target_branch = $2) AND id <> $3`, ia.repo.ID, p.SourceBranch, p.ID).Scan(&others); err != nil {
		slog.Default().Warn("verifica delle PR aperte non riuscita", "err", err)
		return &branchFailure{http.StatusServiceUnavailable, "unavailable", "Verifica delle Pull Request aperte non riuscita."}
	}
	if others > 0 {
		return &branchFailure{http.StatusConflict, "branch_in_use", "Il branch è usato da un'altra Pull Request aperta."}
	}
	_, pusher := s.mergeActor(ctx, ia)
	err := merger.DeleteBranch(ctx, ia.caller, ia.repo.ID, gitclient.DeleteBranchRequest{
		Branch: p.SourceBranch, ExpectedSha: sha40Or(p.HeadSha), Owner: ia.repo.OwnerName, Name: ia.repo.Name,
		DefaultBranch: ia.repo.DefaultBranch, Pusher: pusher})
	var me *gitclient.MergeError
	switch {
	case err == nil:
	case errors.As(err, &me) && me.Status == http.StatusNotFound:
		return &branchFailure{http.StatusNotFound, "branch_not_found", "Il branch è già stato eliminato."}
	case errors.As(err, &me) && me.Code == "stale":
		return &branchFailure{http.StatusConflict, "branch_changed", "Il branch ha ricevuto nuovi commit dopo il merge: non si elimina."}
	case errors.As(err, &me) && me.Code == "default_branch":
		return &branchFailure{http.StatusConflict, "branch_in_use", "Il branch principale non si elimina."}
	default:
		slog.Default().Warn("eliminazione del branch non riuscita", "err", err)
		return &branchFailure{http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile."}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO core.pull_request_events (id, pull_request_id, type, actor_id, data, created_at)
		VALUES ($1, $2, 'branch_deleted', $3, jsonb_build_object('branch', $4::text), clock_timestamp())`,
		uuid.New(), p.ID, ia.userID, p.SourceBranch); err != nil {
		slog.Default().Warn("evento branch_deleted non scritto", "err", err)
	}
	return nil
}

// sha40Or ritorna lo sha se c'è, altrimenti stringa vuota (nessun controllo).
func sha40Or(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DeletePullRequestBranch implementa DELETE …/pulls/{number}/branch.
func (s *apiServer) DeletePullRequestBranch(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Eliminare il branch richiede il permesso write.")
		return
	}
	p, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	merger, ok := s.pullMerger(w)
	if !ok {
		return
	}
	if f := s.deleteMergedBranch(ctx, ia, merger, p); f != nil {
		f.write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
