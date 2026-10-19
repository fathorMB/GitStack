package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Stati dei commit (v1.1, GIT-264, PR9): un sistema esterno, o la CI della
// v2-a, scrive pending/success/failure/error per un contesto su uno sha; chi
// legge il repo vede l'ultimo stato di ogni contesto, la sintesi e i contesti
// obbligatori del branch principale (PR4). Per ogni (repo, sha, contesto) c'è
// una sola riga: un nuovo stato la sostituisce, non resta uno storico.

const (
	maxStatusContextRunes     = 255
	maxStatusDescriptionRunes = 140
	maxStatusTargetURLLen     = 2048
	defaultStatusContext      = "default"
)

// statusRow è una riga di core.commit_statuses.
type statusRow struct {
	ID          uuid.UUID
	Sha         string
	Context     string
	State       string
	Description string
	TargetURL   string
	CreatorID   uuid.UUID
	UpdatedAt   time.Time
}

const statusColumns = `id, sha, context, state, description, target_url, creator_id, updated_at`

func scanStatus(row interface{ Scan(...any) error }) (statusRow, error) {
	var x statusRow
	err := row.Scan(&x.ID, &x.Sha, &x.Context, &x.State, &x.Description, &x.TargetURL, &x.CreatorID, &x.UpdatedAt)
	return x, err
}

func (s *apiServer) statusesAPI(ctx context.Context, rows []statusRow) ([]openapi.CommitStatus, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.CreatorID)
	}
	dir, err := s.resolveUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.CommitStatus, 0, len(rows))
	for _, r := range rows {
		cs := openapi.CommitStatus{
			Id: openapi_types.UUID(r.ID), Sha: r.Sha, State: openapi.CommitStatusState(r.State), Context: r.Context,
			Creator: dir[r.CreatorID], CreatedAt: r.UpdatedAt,
		}
		if r.Description != "" {
			d := r.Description
			cs.Description = &d
		}
		if r.TargetURL != "" {
			u := r.TargetURL
			cs.TargetUrl = &u
		}
		out = append(out, cs)
	}
	return out, nil
}

// combinedState è la sintesi degli ultimi stati dei contesti (PR9): failure se
// uno è failure, altrimenti error se uno è error, altrimenti pending se uno è in
// corso, altrimenti success; none se non ce ne sono.
func combinedState(states []string) openapi.CombinedCommitStatusState {
	if len(states) == 0 {
		return openapi.CombinedCommitStatusStateNone
	}
	has := func(v string) bool { return slices.Contains(states, v) }
	switch {
	case has("failure"):
		return openapi.CombinedCommitStatusStateFailure
	case has("error"):
		return openapi.CombinedCommitStatusStateError
	case has("pending"):
		return openapi.CombinedCommitStatusStatePending
	}
	return openapi.CombinedCommitStatusStateSuccess
}

// commitStatusStates sono gli ultimi stati (uno per contesto) di uno sha.
func commitStatusStates(ctx context.Context, q querier, repoID uuid.UUID, sha string) (map[string]string, error) {
	rows, err := q.Query(ctx, `SELECT context, state FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2`, repoID, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, st string
		if err := rows.Scan(&c, &st); err != nil {
			return nil, err
		}
		out[c] = st
	}
	return out, rows.Err()
}

// pullStatusState è lo stato complessivo di uno sha, per il campo statusState
// della PR (PR9).
func pullStatusState(ctx context.Context, q querier, repoID uuid.UUID, sha string) (*openapi.CombinedCommitStatusState, error) {
	byCtx, err := commitStatusStates(ctx, q, repoID, sha)
	if err != nil {
		return nil, err
	}
	states := make([]string, 0, len(byCtx))
	for _, st := range byCtx {
		states = append(states, st)
	}
	st := combinedState(states)
	return &st, nil
}

// resolveStatusSha risolve lo sha (completo o prefisso) di un commit del repo
// con il servizio git. ok=false dopo aver risposto (400 se non è esadecimale,
// 404 se non esiste).
func (s *apiServer) resolveStatusSha(w http.ResponseWriter, ctx context.Context, ia issueAccess, sha string) (string, bool) {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Letture del codice non disponibili.")
		return "", false
	}
	var out struct {
		Sha string `json:"sha"`
	}
	raw, err := rd.ReadJSON(ctx, ia.caller, ia.repo.ID, "commits/"+url.PathEscape(sha)+"/resolve", nil)
	if err != nil {
		gitFailure(w, err)
		return "", false
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Sha) < 40 {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile.")
		return "", false
	}
	return out.Sha, true
}

// GetCombinedCommitStatus implementa GET …/commits/{sha}/status.
func (s *apiServer) GetCombinedCommitStatus(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, shaParam openapi.CommitShaParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if s.git == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non configurato.")
		return
	}
	ctx := r.Context()
	sha, ok := s.resolveStatusSha(w, ctx, ia, shaParam)
	if !ok {
		return
	}
	bs, err := loadBranchSettings(ctx, s.pool, ia.repo.ID)
	if err != nil {
		writeIssueFailure(w, "lettura delle impostazioni non riuscita", err)
		return
	}
	out, err := s.combinedStatus(ctx, ia.repo.ID, sha, bs.contexts)
	if err != nil {
		writeIssueFailure(w, "lettura degli stati non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// combinedStatus compone la risposta: l'ultimo stato di ogni contesto, la
// sintesi e i contesti obbligatori con il loro stato.
func (s *apiServer) combinedStatus(ctx context.Context, repoID uuid.UUID, sha string, required []string) (openapi.CombinedCommitStatus, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+statusColumns+` FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2
		ORDER BY updated_at DESC, context`, repoID, sha)
	if err != nil {
		return openapi.CombinedCommitStatus{}, err
	}
	var list []statusRow
	for rows.Next() {
		x, err := scanStatus(rows)
		if err != nil {
			rows.Close()
			return openapi.CombinedCommitStatus{}, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return openapi.CombinedCommitStatus{}, err
	}
	items, err := s.statusesAPI(ctx, list)
	if err != nil {
		return openapi.CombinedCommitStatus{}, err
	}
	states := make([]string, 0, len(list))
	byCtx := map[string]string{}
	for _, x := range list {
		states = append(states, x.State)
		byCtx[x.Context] = x.State
	}
	req := make([]openapi.RequiredStatusContext, 0, len(required))
	for _, c := range required {
		rc := openapi.RequiredStatusContext{Context: c}
		if st, ok := byCtx[c]; ok {
			v := openapi.CommitStatusState(st)
			rc.State = &v
			rc.Satisfied = st == "success"
		}
		req = append(req, rc)
	}
	return openapi.CombinedCommitStatus{Sha: sha, State: combinedState(states), Statuses: items, TotalCount: len(items), RequiredContexts: req}, nil
}

// ListCommitStatuses implementa GET …/commits/{sha}/statuses.
func (s *apiServer) ListCommitStatuses(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, shaParam openapi.CommitShaParam, params openapi.ListCommitStatusesParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if s.git == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non configurato.")
		return
	}
	page, perPage, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ctx := r.Context()
	sha, ok := s.resolveStatusSha(w, ctx, ia, shaParam)
	if !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2`, ia.repo.ID, sha).Scan(&total); err != nil {
		writeIssueFailure(w, "lettura degli stati non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+statusColumns+` FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2
		ORDER BY updated_at DESC, context LIMIT $3 OFFSET $4`, ia.repo.ID, sha, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura degli stati non riuscita", err)
		return
	}
	var list []statusRow
	for rows.Next() {
		x, err := scanStatus(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura degli stati non riuscita", err)
			return
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura degli stati non riuscita", err)
		return
	}
	items, err := s.statusesAPI(ctx, list)
	if err != nil {
		writeIssueFailure(w, "lettura degli stati non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.CommitStatusList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// CreateCommitStatus implementa POST …/commits/{sha}/statuses: serve write
// (anche con un token con lo scope write:resource, che il gateway verifica).
func (s *apiServer) CreateCommitStatus(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, shaParam openapi.CommitShaParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Inviare lo stato di un commit richiede il permesso write.")
		return
	}
	if s.git == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non configurato.")
		return
	}
	ctx := r.Context()
	var in openapi.CreateCommitStatusInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	switch in.State {
	case "pending", "success", "failure", "error":
	default:
		writeFieldError(w, "state", "state: pending, success, failure o error.")
		return
	}
	sctx := defaultStatusContext
	if in.Context != nil {
		sctx = strings.TrimSpace(*in.Context)
	}
	if sctx == "" || utf8.RuneCountInString(sctx) > maxStatusContextRunes || strings.ContainsRune(sctx, 0) || !utf8.ValidString(sctx) {
		writeFieldError(w, "context", "context: da 1 a 255 caratteri.")
		return
	}
	var description, target string
	if in.Description != nil {
		description = *in.Description
	}
	if utf8.RuneCountInString(description) > maxStatusDescriptionRunes || strings.ContainsRune(description, 0) || !utf8.ValidString(description) {
		writeFieldError(w, "description", "description: al massimo 140 caratteri.")
		return
	}
	if in.TargetUrl != nil && *in.TargetUrl != "" {
		target = *in.TargetUrl
		u, err := url.Parse(target)
		if err != nil || len(target) > maxStatusTargetURLLen || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			strings.ContainsAny(target, "\x00\r\n") {
			writeFieldError(w, "targetUrl", "targetUrl: un indirizzo http o https valido.")
			return
		}
	}
	sha, ok := s.resolveStatusSha(w, ctx, ia, shaParam)
	if !ok {
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
	row := tx.QueryRow(ctx, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, description, target_url, creator_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (repo_id, sha, context) DO UPDATE SET state = EXCLUDED.state, description = EXCLUDED.description,
			target_url = EXCLUDED.target_url, creator_id = EXCLUDED.creator_id, updated_at = now()
		RETURNING `+statusColumns, uuid.New(), ia.repo.ID, sha, sctx, string(in.State), description, target, ia.userID)
	x, err := scanStatus(row)
	if err != nil {
		writeIssueFailure(w, "scrittura dello stato non riuscita", fmt.Errorf("commit_statuses: %w", err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit dello stato non riuscito", err)
		return
	}
	items, err := s.statusesAPI(ctx, []statusRow{x})
	if err != nil {
		writeIssueFailure(w, "lettura dello stato non riuscita", err)
		return
	}
	writeJSON(w, http.StatusCreated, items[0])
}

// statusGateBlockers sono i blocchi al merge dovuti ai contesti obbligatori
// (PR9): un contesto mai arrivato o ancora pending è status_required, uno in
// failure o error è status_failed.
func statusGateBlockers(required []string, byCtx map[string]string) []openapi.MergeBlocker {
	var missing, failed []string
	for _, c := range required {
		switch byCtx[c] {
		case "success":
		case "failure", "error":
			failed = append(failed, c)
		default:
			missing = append(missing, c)
		}
	}
	var out []openapi.MergeBlocker
	if len(failed) > 0 {
		out = append(out, openapi.MergeBlocker{Code: openapi.MergeBlockerCodeStatusFailed, Contexts: &failed,
			Message: "Controlli obbligatori falliti: " + strings.Join(failed, ", ") + "."})
	}
	if len(missing) > 0 {
		out = append(out, openapi.MergeBlocker{Code: openapi.MergeBlockerCodeStatusRequired, Contexts: &missing,
			Message: "Controlli obbligatori mancanti o ancora in corso: " + strings.Join(missing, ", ") + "."})
	}
	return out
}
