package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/pkg/names"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Mirror in pull di un repo (M-09, MP1-MP8, GIT-195/GIT-197): attivazione,
// stato, «Sincronizza ora», pausa, ripresa, interruzione con conferma,
// registro delle esecuzioni e import da URL. Li gestisce chi ha `admin` sul
// repo o l'amministratore dell'installazione (come i mirror in push): un repo
// che non si legge è 404, chi lo legge senza admin 403. L'aggiornamento vero
// lo fa il motore (internal/mirrors, PullEngine) tramite il servizio git; qui
// si scrive la riga, che è anche la coda. Regole: MP1-MP8 in
// .prisma/knowledge/topics/mirror-in-pull.md.

type pullRow struct {
	id                                uuid.UUID
	origin, mode, state               string
	interval                          int
	username                          *string
	hasToken                          bool
	attempts                          int
	next, lastAttempt, lastOK, stoppd *time.Time
	lastError                         string
	createdBy                         uuid.UUID
	createdAt, updatedAt              time.Time
}

const pullCols = `m.id, m.origin_url, m.mode, m.interval_seconds, m.state, m.username, m.token_ciphertext IS NOT NULL,
	m.attempts, m.next_attempt_at, m.last_attempt_at, m.last_success_at, m.last_error, m.stopped_at, m.created_by,
	m.created_at, m.updated_at`

func scanPull(row pgx.Row) (pullRow, error) {
	var m pullRow
	err := row.Scan(&m.id, &m.origin, &m.mode, &m.interval, &m.state, &m.username, &m.hasToken, &m.attempts, &m.next,
		&m.lastAttempt, &m.lastOK, &m.lastError, &m.stoppd, &m.createdBy, &m.createdAt, &m.updatedAt)
	return m, err
}

func writePullNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Questo repo non ha un mirror in pull.")
}

// loadPullMirror legge il mirror in pull del repo; 404 se non c'è.
func (s *apiServer) loadPullMirror(w http.ResponseWriter, ctx context.Context, q querier, repoID uuid.UUID, forUpdate bool) (pullRow, bool) {
	sql := `SELECT ` + pullCols + ` FROM core.repo_pull_mirrors m WHERE m.repo_id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF m`
	}
	m, err := scanPull(q.QueryRow(ctx, sql, repoID))
	if errors.Is(err, pgx.ErrNoRows) {
		writePullNotFound(w)
		return pullRow{}, false
	}
	if err != nil {
		writeIssueFailure(w, "lettura del mirror in pull non riuscita", err)
		return pullRow{}, false
	}
	return m, true
}

func (s *apiServer) pullView(ctx context.Context, m pullRow) (openapi.PullMirror, error) {
	dir, err := s.resolveUsers(ctx, []uuid.UUID{m.createdBy})
	if err != nil {
		return openapi.PullMirror{}, err
	}
	v := openapi.PullMirror{
		Id: openapi_types.UUID(m.id), Origin: m.origin, Mode: openapi.PullMirrorMode(m.mode), IntervalSeconds: m.interval,
		State: openapi.PullMirrorState(m.state), Username: m.username, HasToken: m.hasToken, Attempts: m.attempts,
		LastAttemptAt: m.lastAttempt, LastSyncedAt: m.lastOK, StoppedAt: m.stoppd,
		DivergedRefs: []openapi.PullMirrorDivergedRef{}, CreatedAt: m.createdAt, UpdatedAt: m.updatedAt,
	}
	switch m.state {
	case mirrors.StatePending, mirrors.StateInSync, mirrors.StateError:
		v.NextSyncAt = m.next
	}
	if m.lastError != "" {
		e := m.lastError
		v.LastError = &e
	}
	if u, ok := dir[m.createdBy]; ok {
		v.CreatedBy = &u
	}
	rows, err := s.pool.Query(ctx, `SELECT ref, kind, local_sha, remote_sha, detected_at FROM core.repo_pull_mirror_refs
		WHERE mirror_id = $1 ORDER BY ref`, m.id)
	if err != nil {
		return openapi.PullMirror{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var d openapi.PullMirrorDivergedRef
		var kind string
		if err := rows.Scan(&d.Ref, &kind, &d.LocalSha, &d.RemoteSha, &d.DetectedAt); err != nil {
			return openapi.PullMirror{}, err
		}
		d.Kind = openapi.PullMirrorDivergedRefKind(kind)
		v.DivergedRefs = append(v.DivergedRefs, d)
	}
	return v, rows.Err()
}

func (s *apiServer) respondPullMirror(w http.ResponseWriter, ctx context.Context, repoID uuid.UUID, status int) {
	m, ok := s.loadPullMirror(w, ctx, s.pool, repoID, false)
	if !ok {
		return
	}
	v, err := s.pullView(ctx, m)
	if err != nil {
		writeIssueFailure(w, "composizione del mirror in pull non riuscita", err)
		return
	}
	writeJSON(w, status, v)
}

// validatePullCredentials: utente e token insieme o nessuno dei due, nei limiti.
func validatePullCredentials(w http.ResponseWriter, username, token *string) bool {
	if !validateMirrorCredential(w, username, token) {
		return false
	}
	if (username == nil) != (token == nil) {
		writeFieldError(w, "token", "username e token vanno indicati insieme, o nessuno dei due.")
		return false
	}
	return true
}

func validPullInterval(w http.ResponseWriter, secs int) bool {
	if secs < int(mirrors.MinPullInterval/time.Second) || secs > int(mirrors.MaxPullInterval/time.Second) {
		writeFieldError(w, "intervalSeconds", "intervalSeconds: da 300 (5 minuti) a 86400 (24 ore).")
		return false
	}
	return true
}

func writePullConflictOrigin(w http.ResponseWriter, what string) {
	writeError(w, http.StatusConflict, "conflict", what)
}

// pushMirrorHasOrigin dice se il repo ha un mirror in push verso l'indirizzo
// (confronto normalizzato, MP6).
func (s *apiServer) pushMirrorHasOrigin(ctx context.Context, repoID uuid.UUID, raw string) (bool, error) {
	want := mirrors.NormalizeOrigin(raw)
	rows, err := s.pool.Query(ctx, `SELECT url FROM core.repo_mirrors WHERE repo_id = $1`, repoID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return false, err
		}
		if mirrors.NormalizeOrigin(u) == want {
			return true, nil
		}
	}
	return false, rows.Err()
}

// pullMirrorHasOrigin dice se il repo ha un mirror in pull attivo (anche in
// pausa) con quell'origine: un mirror in push verso lo stesso indirizzo
// farebbe un giro (MP6).
func (s *apiServer) pullMirrorHasOrigin(ctx context.Context, repoID uuid.UUID, raw string) (bool, error) {
	var origin string
	err := s.pool.QueryRow(ctx, `SELECT origin_url FROM core.repo_pull_mirrors WHERE repo_id = $1 AND state <> 'stopped'`, repoID).Scan(&origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return mirrors.NormalizeOrigin(origin) == mirrors.NormalizeOrigin(raw), nil
}

func (s *apiServer) GetRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.respondPullMirror(w, r.Context(), sc.repo.ID, http.StatusOK)
	}
}

func (s *apiServer) EnableRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	var in openapi.EnablePullMirrorInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !s.validateMirrorURL(w, in.Url) || !validatePullCredentials(w, in.Username, in.Token) {
		return
	}
	interval := int(mirrors.DefaultPullInterval / time.Second)
	if in.IntervalSeconds != nil {
		interval = *in.IntervalSeconds
	}
	if !validPullInterval(w, interval) {
		return
	}
	ctx := r.Context()
	if clash, err := s.pushMirrorHasOrigin(ctx, sc.repo.ID, in.Url); err != nil {
		writeIssueFailure(w, "lettura dei mirror non riuscita", err)
		return
	} else if clash {
		writePullConflictOrigin(w, "Il repo ha già un mirror in push verso questo indirizzo: l'origine non può essere anche la destinazione.")
		return
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "attivazione del mirror in pull non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	id := uuid.New()
	exists := false
	var cur string
	err = tx.QueryRow(ctx, `SELECT id, state FROM core.repo_pull_mirrors WHERE repo_id = $1 FOR UPDATE`, sc.repo.ID).Scan(&id, &cur)
	switch {
	case err == nil:
		if cur != mirrors.StateStopped {
			writeError(w, http.StatusConflict, "conflict", "Il repo ha già un mirror in pull attivo.")
			return
		}
		exists = true
	case errors.Is(err, pgx.ErrNoRows):
	default:
		writeIssueFailure(w, "attivazione del mirror in pull non riuscita", err)
		return
	}
	var ct, nonce []byte
	var keyID *string
	if in.Token != nil {
		c, n, k, ok := s.sealMirrorToken(w, id, *in.Token)
		if !ok {
			return
		}
		ct, nonce, keyID = c, n, &k
	}
	now := s.now()
	if exists {
		// Riattivazione di un mirror interrotto: stessa riga (registro e ref
		// divergenti restano), da capo come stato.
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET origin_url = $2, mode = 'mirror', interval_seconds = $3,
				username = $4, token_ciphertext = $5, token_nonce = $6, token_key_id = $7, state = 'pending', attempts = 0,
				next_attempt_at = $8, last_error = '', last_success_at = NULL, stale_notified_at = NULL, stopped_at = NULL,
				created_by = $9, created_at = $8, updated_at = $8
			WHERE id = $1`, id, in.Url, interval, in.Username, ct, nonce, keyID, now, sc.userID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO core.repo_pull_mirrors
				(id, repo_id, origin_url, mode, interval_seconds, username, token_ciphertext, token_nonce, token_key_id,
				 state, next_attempt_at, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, 'mirror', $4, $5, $6, $7, $8, 'pending', $9, $10, $9, $9)`,
			id, sc.repo.ID, in.Url, interval, in.Username, ct, nonce, keyID, now, sc.userID)
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "conflict", "Il repo ha già un mirror in pull attivo.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "attivazione del mirror in pull non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "attivazione del mirror in pull non riuscita", err)
		return
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusCreated)
}

func (s *apiServer) UpdateRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, true)
	if !ok {
		return
	}
	var in openapi.UpdatePullMirrorInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.IntervalSeconds == nil && in.Username == nil && in.Token == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Indica almeno un campo da modificare.")
		return
	}
	if in.IntervalSeconds != nil && !validPullInterval(w, *in.IntervalSeconds) {
		return
	}
	// Stringa vuota = toglie; altrimenti i limiti sono quelli della creazione.
	var newUser, newToken *string
	if in.Username != nil && *in.Username != "" {
		newUser = in.Username
	}
	if in.Token != nil && *in.Token != "" {
		newToken = in.Token
	}
	if !validateMirrorCredential(w, newUser, newToken) {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "modifica del mirror in pull non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, ok := s.loadPullMirror(w, ctx, tx, sc.repo.ID, true)
	if !ok {
		return
	}
	if m.state == mirrors.StateStopped {
		writeError(w, http.StatusConflict, "mirror_stopped", "Il mirror in pull è interrotto: riattivalo prima di modificarlo.")
		return
	}
	// Credenziali risultanti: utente e token insieme o nessuno dei due.
	user := m.username != nil
	token := m.hasToken
	if in.Username != nil {
		user = newUser != nil
	}
	if in.Token != nil {
		token = newToken != nil
	}
	if user != token {
		writeFieldError(w, "token", "username e token vanno indicati insieme, o nessuno dei due.")
		return
	}
	now := s.now()
	credsChanged := false
	if in.Username != nil {
		if _, err := tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET username = $2 WHERE id = $1`, m.id, newUser); err != nil {
			writeIssueFailure(w, "modifica del mirror in pull non riuscita", err)
			return
		}
		credsChanged = true
	}
	if in.Token != nil {
		if newToken == nil {
			_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET token_ciphertext = NULL, token_nonce = NULL, token_key_id = NULL WHERE id = $1`, m.id)
		} else {
			ct, nonce, keyID, ok := s.sealMirrorToken(w, m.id, *newToken)
			if !ok {
				return
			}
			_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET token_ciphertext = $2, token_nonce = $3, token_key_id = $4 WHERE id = $1`, m.id, ct, nonce, keyID)
		}
		if err != nil {
			writeIssueFailure(w, "modifica del token non riuscita", err)
			return
		}
		credsChanged = true
	}
	if in.IntervalSeconds != nil {
		// Un mirror allineato riparte dal nuovo intervallo dall'ultimo successo
		// (ma mai nel passato: un intervallo più corto lo rende subito dovuto).
		if _, err := tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET interval_seconds = $2::int,
				next_attempt_at = CASE WHEN state = 'in_sync' AND last_success_at IS NOT NULL
					THEN last_success_at + make_interval(secs => $2::int) ELSE next_attempt_at END WHERE id = $1`, m.id, *in.IntervalSeconds); err != nil {
			writeIssueFailure(w, "modifica del mirror in pull non riuscita", err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET updated_at = $2 WHERE id = $1`, m.id, now); err != nil {
		writeIssueFailure(w, "modifica del mirror in pull non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "modifica del mirror in pull non riuscita", err)
		return
	}
	if credsChanged {
		// Credenziali nuove: si riprova subito, anche dopo un errore o un blocco.
		if _, err := mirrors.RequeuePull(ctx, s.pool, now, `id = $2`, m.id); err != nil {
			writeIssueFailure(w, "messa in coda del mirror non riuscita", err)
			return
		}
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusOK)
}

func (s *apiServer) SyncRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	ctx := r.Context()
	m, ok := s.loadPullMirror(w, ctx, s.pool, sc.repo.ID, false)
	if !ok {
		return
	}
	switch m.state {
	case mirrors.StateStopped:
		writeError(w, http.StatusConflict, "mirror_stopped", "Il mirror in pull è interrotto.")
		return
	case mirrors.StatePaused:
		writeError(w, http.StatusConflict, "mirror_paused", "Il mirror in pull è in pausa: riprendilo prima di sincronizzarlo.")
		return
	}
	// In corso (syncing): idempotente, la clausola non lo tocca.
	if _, err := mirrors.RequeuePull(ctx, s.pool, s.now(), `id = $2`, m.id); err != nil {
		writeIssueFailure(w, "messa in coda del mirror non riuscita", err)
		return
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusAccepted)
}

func (s *apiServer) PauseRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	ctx := r.Context()
	m, ok := s.loadPullMirror(w, ctx, s.pool, sc.repo.ID, false)
	if !ok {
		return
	}
	switch m.state {
	case mirrors.StateStopped:
		writeError(w, http.StatusConflict, "mirror_stopped", "Il mirror in pull è interrotto.")
		return
	case mirrors.StatePaused:
		s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusOK)
		return
	}
	// L'affitto resta: un aggiornamento in corso finisce (e si registra), e un
	// «Riprendi» subito dopo non ne fa partire un secondo in parallelo.
	if _, err := s.pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'paused', next_attempt_at = NULL, updated_at = $2
		WHERE id = $1 AND state IN ('pending', 'syncing', 'in_sync', 'error')`, m.id, s.now()); err != nil {
		writeIssueFailure(w, "pausa del mirror in pull non riuscita", err)
		return
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusOK)
}

func (s *apiServer) ResumeRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	ctx := r.Context()
	m, ok := s.loadPullMirror(w, ctx, s.pool, sc.repo.ID, false)
	if !ok {
		return
	}
	switch m.state {
	case mirrors.StateStopped:
		writeError(w, http.StatusConflict, "mirror_stopped", "Il mirror in pull è interrotto.")
		return
	case mirrors.StatePaused:
		if _, err := s.pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'pending', attempts = 0, next_attempt_at = $2, updated_at = $2
			WHERE id = $1 AND state = 'paused'`, m.id, s.now()); err != nil {
			writeIssueFailure(w, "ripresa del mirror in pull non riuscita", err)
			return
		}
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusOK)
}

func (s *apiServer) StopRepoPullMirror(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	var in openapi.StopPullMirrorInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Confirm != sc.repo.Name {
		var body openapi.Error
		body.Error.Code = "confirmation_mismatch"
		body.Error.Message = "La conferma deve essere il nome del repo."
		d := map[string]interface{}{"fields": map[string]interface{}{"confirm": "confirm: deve essere il nome del repo."}}
		body.Error.Details = &d
		writeJSON(w, http.StatusUnprocessableEntity, body)
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "interruzione del mirror in pull non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, ok := s.loadPullMirror(w, ctx, tx, sc.repo.ID, true)
	if !ok {
		return
	}
	if m.state != mirrors.StateStopped {
		// Credenziali via, repo normale; registro e ref divergenti restano.
		if _, err := tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'stopped', stopped_at = $2, next_attempt_at = NULL,
				username = NULL, token_ciphertext = NULL, token_nonce = NULL, token_key_id = NULL, updated_at = $2
			WHERE id = $1`, m.id, s.now()); err != nil {
			writeIssueFailure(w, "interruzione del mirror in pull non riuscita", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			writeIssueFailure(w, "interruzione del mirror in pull non riuscita", err)
			return
		}
	}
	s.respondPullMirror(w, ctx, sc.repo.ID, http.StatusOK)
}

func (s *apiServer) ListRepoPullMirrorRuns(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListRepoPullMirrorRunsParams) {
	sc, ok := s.hookRepoScope(w, r, owner, name, false)
	if !ok {
		return
	}
	pg, pp, ok := pageParams(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ctx := r.Context()
	m, ok := s.loadPullMirror(w, ctx, s.pool, sc.repo.ID, false)
	if !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_pull_mirror_runs WHERE mirror_id = $1`, m.id).Scan(&total); err != nil {
		writeIssueFailure(w, "lettura del registro non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT id, trigger, started_at, finished_at, duration_ms, outcome, attempt, error, refs
		FROM core.repo_pull_mirror_runs WHERE mirror_id = $1 ORDER BY started_at DESC, id LIMIT $2 OFFSET $3`, m.id, pp, (pg-1)*pp)
	if err != nil {
		writeIssueFailure(w, "lettura del registro non riuscita", err)
		return
	}
	defer rows.Close()
	items := []openapi.PullMirrorRun{}
	for rows.Next() {
		var run openapi.PullMirrorRun
		var rid uuid.UUID
		var trigger, outcome, errText string
		var refs []byte
		if err := rows.Scan(&rid, &trigger, &run.StartedAt, &run.FinishedAt, &run.DurationMs, &outcome, &run.Attempt, &errText, &refs); err != nil {
			writeIssueFailure(w, "lettura del registro non riuscita", err)
			return
		}
		run.Id = openapi_types.UUID(rid)
		run.Trigger = openapi.PullMirrorRunTrigger(trigger)
		run.Outcome = openapi.PullMirrorRunOutcome(outcome)
		if errText != "" {
			run.Error = &errText
		}
		run.Refs = []openapi.PullMirrorRunRef{}
		_ = json.Unmarshal(refs, &run.Refs)
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura del registro non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.PullMirrorRunList{Items: items, Page: pg, PerPage: pp, Total: total})
}

// ImportRepository implementa POST /repos/import (MP3, MP8): crea un repo
// nuovo e il suo mirror in pull nella stessa transazione; la copia parte in
// background (il motore prende la riga). Permessi e nome come createRepository.
func (s *apiServer) ImportRepository(w http.ResponseWriter, r *http.Request) {
	var in openapi.ImportRepositoryInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Owner == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "owner è obbligatorio.")
		return
	}
	if err := names.ValidateRepoName(in.Name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Nome del repo non valido: "+err.Error())
		return
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
		if utf8.RuneCountInString(description) > maxDescriptionRunes {
			writeError(w, http.StatusBadRequest, "invalid_body", "description supera i 1024 caratteri.")
			return
		}
	}
	visibility := string(openapi.RepoVisibilityPrivate) // P7
	if in.Visibility != nil {
		visibility = string(*in.Visibility)
		if !validVisibility(visibility) {
			writeError(w, http.StatusBadRequest, "invalid_body", "visibility deve essere private o internal.")
			return
		}
	}
	if in.Mode != openapi.PullMirrorModeMirror && in.Mode != openapi.PullMirrorModeOnce {
		writeFieldError(w, "mode", "mode: mirror oppure once.")
		return
	}
	if !s.validateMirrorURL(w, in.Url) || !validatePullCredentials(w, in.Username, in.Token) {
		return
	}
	interval := int(mirrors.DefaultPullInterval / time.Second)
	if in.IntervalSeconds != nil && in.Mode == openapi.PullMirrorModeMirror {
		interval = *in.IntervalSeconds
		if !validPullInterval(w, interval) {
			return
		}
	}
	mirrorID := uuid.New()
	var ct, nonce []byte
	var keyID *string
	if in.Token != nil {
		c, n, k, ok := s.sealMirrorToken(w, mirrorID, *in.Token)
		if !ok {
			return
		}
		ct, nonce, keyID = c, n, &k
	}
	now := s.now()
	s.createRepo(w, r, createRepoParams{
		body: openapi.CreateRepositoryInput{
			Owner: in.Owner, Name: in.Name, Description: in.Description, Visibility: in.Visibility,
		},
		description: description,
		visibility:  visibility,
		inTx: func(ctx context.Context, tx pgx.Tx, repo store.Repo, creator uuid.UUID) error {
			_, err := tx.Exec(ctx, `INSERT INTO core.repo_pull_mirrors
					(id, repo_id, origin_url, mode, interval_seconds, username, token_ciphertext, token_nonce, token_key_id,
					 state, next_attempt_at, created_by, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10, $11, $10, $10)`,
				mirrorID, repo.ID, in.Url, string(in.Mode), interval, in.Username, ct, nonce, keyID, now, creator)
			return err
		},
	})
}

// fillRepoMirrors imposta `mirror` (MP2, MP7) sui repo di un elenco che hanno
// un mirror in pull attivo (anche in pausa; non interrotto), con una sola
// query. Un errore di lettura lascia il campo vuoto e si logga: i metadati
// del repo restano leggibili.
func (s *apiServer) fillRepoMirrors(ctx context.Context, repos []openapi.Repository) {
	if s.pool == nil || len(repos) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(repos))
	for _, r := range repos {
		if r.Id != nil {
			ids = append(ids, uuid.UUID(*r.Id))
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT repo_id, origin_url, last_success_at FROM core.repo_pull_mirrors
		WHERE repo_id = ANY($1) AND state <> 'stopped'`, ids)
	if err != nil {
		slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", err)
		return
	}
	defer rows.Close()
	found := make(map[uuid.UUID]*openapi.RepositoryMirror, len(ids))
	for rows.Next() {
		var id uuid.UUID
		m := &openapi.RepositoryMirror{}
		if err := rows.Scan(&id, &m.Origin, &m.LastSyncedAt); err != nil {
			slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", err)
			return
		}
		found[id] = m
	}
	if rows.Err() != nil {
		slog.Warn("lettura dei mirror in pull dei repo non riuscita", "err", rows.Err())
		return
	}
	for i := range repos {
		if repos[i].Id != nil {
			repos[i].Mirror = found[uuid.UUID(*repos[i].Id)]
		}
	}
}

// repoWithMirror è toAPIRepo più il campo `mirror`.
func (s *apiServer) repoWithMirror(req *http.Request, r store.Repo, empty bool) openapi.Repository {
	one := []openapi.Repository{s.toAPIRepo(req, r, empty)}
	s.fillRepoMirrors(req.Context(), one)
	return one[0]
}

// rejectPullOrigin risponde 409 (e ritorna true) se url è l'origine del
// mirror in pull attivo del repo: un mirror in push verso l'origine
// ricopierebbe il repo su se stesso (MP6).
func (s *apiServer) rejectPullOrigin(w http.ResponseWriter, ctx context.Context, repoID uuid.UUID, raw string) bool {
	clash, err := s.pullMirrorHasOrigin(ctx, repoID, raw)
	if err != nil {
		writeIssueFailure(w, "lettura del mirror in pull non riuscita", err)
		return true
	}
	if clash {
		writePullConflictOrigin(w, "Il repo è un mirror in pull di questo indirizzo: non si può anche spingere verso l'origine.")
		return true
	}
	return false
}
