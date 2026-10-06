package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Mirror in push di un repo (M-04/V8, GIT-179): CRUD, «Sincronizza ora», stato
// e log delle esecuzioni. Li gestisce chi ha `admin` sul repo; un repo che non
// si legge è 404, chi lo legge senza admin 403. Il push vero lo fa il motore
// (internal/mirrors) tramite il servizio git; qui si scrive la riga, che è
// anche la coda.

// MirrorConfig imposta cifratura del token e controllo della destinazione.
type MirrorConfig struct {
	// Keys cifra il token; nil: creare un mirror risponde 503.
	Keys *webhooks.Keyring
	// Egress controlla gli indirizzi (C8); nil: solo i controlli sul testo
	// (https, niente credenziali).
	Egress *mirrors.Egress
}

const (
	maxMirrorUsername = 256
	maxMirrorToken    = 512
)

type mirrorRow struct {
	id            uuid.UUID
	url, username string
	enabled       bool
	state         string
	pending       bool
	attempts      int
	nextAttemptAt time.Time
	lastAttemptAt *time.Time
	lastSuccessAt *time.Time
	lastError     string
	lastPushed    []byte
	createdBy     uuid.UUID
	createdAt     time.Time
	updatedAt     time.Time
}

const mirrorCols = `m.id, m.url, m.username, m.enabled, m.state, m.pending, m.attempts, m.next_attempt_at, m.last_attempt_at,
	m.last_success_at, m.last_error, m.last_pushed, m.created_by, m.created_at, m.updated_at`

func scanMirror(row pgx.Row) (mirrorRow, error) {
	var m mirrorRow
	err := row.Scan(&m.id, &m.url, &m.username, &m.enabled, &m.state, &m.pending, &m.attempts, &m.nextAttemptAt, &m.lastAttemptAt,
		&m.lastSuccessAt, &m.lastError, &m.lastPushed, &m.createdBy, &m.createdAt, &m.updatedAt)
	return m, err
}

func (s *apiServer) mirrorViews(ctx context.Context, rows []mirrorRow) ([]openapi.RepoMirror, error) {
	users := make([]uuid.UUID, 0, len(rows))
	for _, m := range rows {
		users = append(users, m.createdBy)
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.RepoMirror, 0, len(rows))
	for _, m := range rows {
		v := openapi.RepoMirror{
			Id: openapi_types.UUID(m.id), Url: m.url, Username: m.username, HasToken: true, Enabled: m.enabled,
			State: openapi.RepoMirrorState(m.state), Attempts: m.attempts, LastAttemptAt: m.lastAttemptAt, LastSuccessAt: m.lastSuccessAt,
			LastPushed: map[string]string{}, CreatedAt: m.createdAt, UpdatedAt: m.updatedAt,
		}
		_ = json.Unmarshal(m.lastPushed, &v.LastPushed)
		if v.LastPushed == nil {
			v.LastPushed = map[string]string{}
		}
		if m.lastError != "" {
			e := m.lastError
			v.LastError = &e
		}
		if m.enabled && m.pending && m.state != mirrors.StateDiverged {
			t := m.nextAttemptAt
			v.NextAttemptAt = &t
		}
		if u, ok := dir[m.createdBy]; ok {
			v.CreatedBy = &u
		}
		out = append(out, v)
	}
	return out, nil
}

func writeMirrorNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Mirror non trovato.")
}

func (s *apiServer) loadMirror(w http.ResponseWriter, ctx context.Context, q querier, repoID, id uuid.UUID, forUpdate bool) (mirrorRow, bool) {
	sql := `SELECT ` + mirrorCols + ` FROM core.repo_mirrors m WHERE m.repo_id = $1 AND m.id = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF m`
	}
	m, err := scanMirror(q.QueryRow(ctx, sql, repoID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeMirrorNotFound(w)
		return mirrorRow{}, false
	}
	if err != nil {
		writeIssueFailure(w, "lettura del mirror non riuscita", err)
		return mirrorRow{}, false
	}
	return m, true
}

func (s *apiServer) respondMirror(w http.ResponseWriter, ctx context.Context, repoID, id uuid.UUID, status int) {
	m, ok := s.loadMirror(w, ctx, s.pool, repoID, id, false)
	if !ok {
		return
	}
	views, err := s.mirrorViews(ctx, []mirrorRow{m})
	if err != nil {
		writeIssueFailure(w, "composizione del mirror non riuscita", err)
		return
	}
	writeJSON(w, status, views[0])
}

// validateMirrorURL: https, senza credenziali, ammesso da C8 (422 url_not_allowed).
func (s *apiServer) validateMirrorURL(w http.ResponseWriter, raw string) bool {
	var err error
	if s.mirrors.Egress != nil {
		err = s.mirrors.Egress.CheckURL(raw)
	} else {
		_, err = mirrors.ParseURL(raw)
	}
	if err != nil {
		if errors.Is(err, mirrors.ErrURLNotAllowed) {
			writeHookURLError(w, err.Error())
			return false
		}
		writeFieldError(w, "url", "url: indirizzo non valido.")
		return false
	}
	return true
}

func validateMirrorCredential(w http.ResponseWriter, username, token *string) bool {
	if username != nil && (*username == "" || len(*username) > maxMirrorUsername) {
		writeFieldError(w, "username", "username: da 1 a 256 caratteri.")
		return false
	}
	if token != nil && (*token == "" || len(*token) > maxMirrorToken) {
		writeFieldError(w, "token", "token: da 1 a 512 caratteri.")
		return false
	}
	return true
}

// sealMirrorToken cifra il token; ok=false dopo aver risposto.
func (s *apiServer) sealMirrorToken(w http.ResponseWriter, id uuid.UUID, token string) (ct, nonce []byte, keyID string, ok bool) {
	ct, nonce, keyID, err := s.mirrors.Keys.Seal(id, token)
	if errors.Is(err, webhooks.ErrNoKey) {
		writeError(w, http.StatusServiceUnavailable, "mirror_secrets_unavailable", "Le credenziali dei mirror non sono configurate su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return nil, nil, "", false
	}
	if err != nil {
		writeIssueFailure(w, "cifratura del token non riuscita", err)
		return nil, nil, "", false
	}
	return ct, nonce, keyID, true
}

func (s *apiServer) ListRepoMirrors(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListRepoMirrorsParams) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	pg, pp, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ctx := r.Context()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_mirrors m WHERE m.repo_id = $1`, sc.repo.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "lettura dei mirror non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+mirrorCols+` FROM core.repo_mirrors m WHERE m.repo_id = $1
		ORDER BY m.created_at, m.id LIMIT $2 OFFSET $3`, sc.repo.ID, pp, (pg-1)*pp)
	if err != nil {
		writeIssueFailure(w, "lettura dei mirror non riuscita", err)
		return
	}
	var list []mirrorRow
	for rows.Next() {
		m, err := scanMirror(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura dei mirror non riuscita", err)
			return
		}
		list = append(list, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura dei mirror non riuscita", err)
		return
	}
	views, err := s.mirrorViews(ctx, list)
	if err != nil {
		writeIssueFailure(w, "composizione dei mirror non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.RepoMirrorList{Items: views, Page: pg, PerPage: pp, Total: total})
}

func (s *apiServer) CreateRepoMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	var in openapi.CreateRepoMirrorInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !s.validateMirrorURL(w, in.Url) {
		return
	}
	if in.Token == nil {
		writeFieldError(w, "token", "token: obbligatorio.")
		return
	}
	if !validateMirrorCredential(w, &in.Username, in.Token) {
		return
	}
	id := uuid.New()
	ct, nonce, keyID, ok := s.sealMirrorToken(w, id, *in.Token)
	if !ok {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	_, err := s.pool.Exec(r.Context(), `INSERT INTO core.repo_mirrors
			(id, repo_id, url, username, token_ciphertext, token_nonce, token_key_id, enabled, state, pending, next_attempt_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $8, $9, $10)`,
		id, sc.repo.ID, in.Url, in.Username, ct, nonce, keyID, enabled, s.now(), sc.userID)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "conflict", "Esiste già un mirror con questo indirizzo per il repo.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "creazione del mirror non riuscita", err)
		return
	}
	s.respondMirror(w, r.Context(), sc.repo.ID, id, http.StatusCreated)
}

func (s *apiServer) GetRepoMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.MirrorIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.respondMirror(w, r.Context(), sc.repo.ID, id, http.StatusOK)
	}
}

func (s *apiServer) UpdateRepoMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.MirrorIdParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	var in openapi.UpdateRepoMirrorInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Url != nil && !s.validateMirrorURL(w, *in.Url) {
		return
	}
	if !validateMirrorCredential(w, in.Username, in.Token) {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "modifica del mirror non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, ok := s.loadMirror(w, ctx, tx, sc.repo.ID, id, true)
	if !ok {
		return
	}
	// Cambiare destinazione o credenziale, o riattivare, rimette in coda il
	// mirror da zero: lo stato vecchio non vale più per la destinazione nuova.
	requeue := false
	urlChanged := false
	if in.Url != nil && *in.Url != m.url {
		m.url, urlChanged, requeue = *in.Url, true, true
	}
	if in.Username != nil && *in.Username != m.username {
		m.username, requeue = *in.Username, true
	}
	if in.Enabled != nil && *in.Enabled && !m.enabled {
		requeue = true
	}
	if in.Enabled != nil {
		m.enabled = *in.Enabled
	}
	if in.Token != nil {
		ct, nonce, keyID, ok := s.sealMirrorToken(w, id, *in.Token)
		if !ok {
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE core.repo_mirrors SET token_ciphertext = $2, token_nonce = $3, token_key_id = $4 WHERE id = $1`, id, ct, nonce, keyID); err != nil {
			writeIssueFailure(w, "modifica del token non riuscita", err)
			return
		}
		requeue = true
	}
	if _, err := tx.Exec(ctx, `UPDATE core.repo_mirrors SET url = $2, username = $3, enabled = $4, updated_at = $5 WHERE id = $1`,
		id, m.url, m.username, m.enabled, s.now()); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "Esiste già un mirror con questo indirizzo per il repo.")
			return
		}
		writeIssueFailure(w, "modifica del mirror non riuscita", err)
		return
	}
	if urlChanged {
		if _, err := tx.Exec(ctx, `UPDATE core.repo_mirrors SET last_success_at = NULL, last_error = '', last_pushed = '{}'::jsonb WHERE id = $1`, id); err != nil {
			writeIssueFailure(w, "modifica del mirror non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "modifica del mirror non riuscita", err)
		return
	}
	if requeue && m.enabled {
		if _, err := mirrors.Requeue(ctx, s.pool, s.now(), true, `id = $3`, id); err != nil {
			writeIssueFailure(w, "messa in coda del mirror non riuscita", err)
			return
		}
	}
	s.respondMirror(w, ctx, sc.repo.ID, id, http.StatusOK)
}

func (s *apiServer) DeleteRepoMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.MirrorIdParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM core.repo_mirrors WHERE repo_id = $1 AND id = $2`, sc.repo.ID, id)
	if err != nil {
		writeIssueFailure(w, "eliminazione del mirror non riuscita", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeMirrorNotFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) SyncRepoMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.MirrorIdParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	ctx := r.Context()
	m, ok := s.loadMirror(w, ctx, s.pool, sc.repo.ID, id, false)
	if !ok {
		return
	}
	if !m.enabled {
		writeError(w, http.StatusConflict, "mirror_disabled", "Il mirror è in pausa: riattivalo prima di sincronizzarlo.")
		return
	}
	if _, err := mirrors.Requeue(ctx, s.pool, s.now(), true, `id = $3 AND enabled`, id); err != nil {
		writeIssueFailure(w, "messa in coda del mirror non riuscita", err)
		return
	}
	s.respondMirror(w, ctx, sc.repo.ID, id, http.StatusAccepted)
}

func (s *apiServer) ListRepoMirrorRuns(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.MirrorIdParam, params openapi.ListRepoMirrorRunsParams) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	pg, pp, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.loadMirror(w, ctx, s.pool, sc.repo.ID, id, false); !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_mirror_runs WHERE mirror_id = $1`, id).Scan(&total); err != nil {
		writeIssueFailure(w, "lettura del log non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT id, started_at, finished_at, duration_ms, outcome, attempt, error, refs
		FROM core.repo_mirror_runs WHERE mirror_id = $1 ORDER BY started_at DESC, id LIMIT $2 OFFSET $3`, id, pp, (pg-1)*pp)
	if err != nil {
		writeIssueFailure(w, "lettura del log non riuscita", err)
		return
	}
	defer rows.Close()
	items := []openapi.RepoMirrorRun{}
	for rows.Next() {
		var run openapi.RepoMirrorRun
		var rid uuid.UUID
		var outcome, errText string
		var refs []byte
		if err := rows.Scan(&rid, &run.StartedAt, &run.FinishedAt, &run.DurationMs, &outcome, &run.Attempt, &errText, &refs); err != nil {
			writeIssueFailure(w, "lettura del log non riuscita", err)
			return
		}
		run.Id = openapi_types.UUID(rid)
		run.Outcome = openapi.RepoMirrorRunOutcome(outcome)
		if errText != "" {
			run.Error = &errText
		}
		run.Refs = []openapi.RepoMirrorRunRef{}
		_ = json.Unmarshal(refs, &run.Refs)
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura del log non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.RepoMirrorRunList{Items: items, Page: pg, PerPage: pp, Total: total})
}
