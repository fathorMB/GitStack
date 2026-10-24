package ci

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner esterni (GIT-278, CI2, ADR 0001 §4). Due credenziali, mai in chiaro nel database:
//   - il token di registrazione (gsrt_…): lo crea l'amministratore, vale una volta sola;
//   - il token del runner (gsr_…): lo riceve alla registrazione e vale solo per FetchTask e Declare.
const (
	RegistrationTokenPrefix = "gsrt_"
	RunnerTokenPrefix       = "gsr_"
	// RegistrationTokenTTL è la validità di un token di registrazione non usato.
	RegistrationTokenTTL = time.Hour
	// RunnerOnlineWindow: un runner che non chiama FetchTask da più di così è offline.
	RunnerOnlineWindow = 90 * time.Second

	maxRunnerLabels = 32
	maxLabelLen     = 64
)

var (
	ErrRegistrationToken = errors.New("token di registrazione non valido")
	ErrRunnerToken       = errors.New("token del runner non valido")
	ErrRunnerName        = errors.New("nome del runner già in uso")
	ErrRunnerNotFound    = errors.New("runner non trovato")
	ErrRunnerInput       = errors.New("nome o etichette del runner non valide")
)

var (
	runnerNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	runnerLabelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// RunnerRow è un runner esterno registrato.
type RunnerRow struct {
	ID         uuid.UUID
	Name       string
	Labels     []string
	Version    string
	Status     string // online, offline, busy
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

func newToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// IssueRegistrationToken crea un token monouso di registrazione. Il valore in chiaro
// è solo nel ritorno: nel database c'è lo SHA-256.
func IssueRegistrationToken(ctx context.Context, pool *pgxpool.Pool, now time.Time, createdBy *uuid.UUID) (string, time.Time, error) {
	token, err := newToken(RegistrationTokenPrefix)
	if err != nil {
		return "", time.Time{}, err
	}
	exp := now.Add(RegistrationTokenTTL)
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_runner_registration_tokens (id, token_hash, created_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, uuid.New(), hashJobToken(token), createdBy, now, exp); err != nil {
		return "", time.Time{}, err
	}
	// Pulizia dei token scaduti da più di un giorno.
	_, _ = pool.Exec(ctx, `DELETE FROM core.ci_runner_registration_tokens WHERE expires_at < $1`, now.Add(-24*time.Hour))
	return token, exp, nil
}

// ValidateRunnerInput controlla il nome e normalizza le etichette. «self-hosted» c'è sempre.
func ValidateRunnerInput(name string, labels []string) ([]string, error) {
	if !runnerNameRe.MatchString(name) {
		return nil, ErrRunnerInput
	}
	norm := NormalizeLabels(append([]string{"self-hosted"}, labels...))
	if len(norm) > maxRunnerLabels {
		return nil, ErrRunnerInput
	}
	for _, l := range norm {
		if len(l) > maxLabelLen || !runnerLabelRe.MatchString(l) {
			return nil, ErrRunnerInput
		}
	}
	return norm, nil
}

// RegisterRunner consuma il token di registrazione e crea il runner, nella stessa
// transazione: se il nome è già usato il token non si consuma. Ritorna il token del runner.
func RegisterRunner(ctx context.Context, pool *pgxpool.Pool, now time.Time, regToken, name, version string, labels []string) (RunnerRow, string, error) {
	if !strings.HasPrefix(regToken, RegistrationTokenPrefix) || len(regToken) > 128 {
		return RunnerRow{}, "", ErrRegistrationToken
	}
	norm, err := ValidateRunnerInput(name, labels)
	if err != nil {
		return RunnerRow{}, "", err
	}
	if len(version) > 64 {
		version = version[:64]
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return RunnerRow{}, "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE core.ci_runner_registration_tokens SET consumed_at = $2
		WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > $2`, hashJobToken(regToken), now)
	if err != nil {
		return RunnerRow{}, "", err
	}
	if tag.RowsAffected() != 1 {
		return RunnerRow{}, "", ErrRegistrationToken
	}
	token, err := newToken(RunnerTokenPrefix)
	if err != nil {
		return RunnerRow{}, "", err
	}
	r := RunnerRow{ID: uuid.New(), Name: name, Labels: norm, Version: version, CreatedAt: now, Status: "offline"}
	if _, err := tx.Exec(ctx, `INSERT INTO core.ci_runners (id, name, labels, version, token_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, r.ID, name, norm, version, hashJobToken(token), now); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return RunnerRow{}, "", ErrRunnerName
		}
		return RunnerRow{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return RunnerRow{}, "", err
	}
	return r, token, nil
}

// ResolveRunner verifica il token del runner e ne aggiorna last_seen_at.
func ResolveRunner(ctx context.Context, pool *pgxpool.Pool, now time.Time, token string) (RunnerRow, error) {
	if !strings.HasPrefix(token, RunnerTokenPrefix) || len(token) > 128 {
		return RunnerRow{}, ErrRunnerToken
	}
	var r RunnerRow
	err := pool.QueryRow(ctx, `UPDATE core.ci_runners SET last_seen_at = $2 WHERE token_hash = $1
		RETURNING id, name, labels, version, created_at`, hashJobToken(token), now).
		Scan(&r.ID, &r.Name, &r.Labels, &r.Version, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunnerRow{}, ErrRunnerToken
	}
	if err != nil {
		return RunnerRow{}, err
	}
	return r, nil
}

// DeclareRunner aggiorna etichette e versione di un runner (Declare).
func DeclareRunner(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, version string, labels []string) ([]string, error) {
	norm, err := ValidateRunnerInput("x", labels)
	if err != nil {
		return nil, err
	}
	if len(version) > 64 {
		version = version[:64]
	}
	if _, err := pool.Exec(ctx, `UPDATE core.ci_runners SET labels = $2, version = $3 WHERE id = $1`, id, norm, version); err != nil {
		return nil, err
	}
	return norm, nil
}

// ListRunners elenca i runner. Stato: busy se ha un job in corso, online se si è fatto
// vivo di recente, altrimenti offline.
func ListRunners(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]RunnerRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.id, r.name, r.labels, r.version, r.created_at, r.last_seen_at,
			CASE WHEN EXISTS (SELECT 1 FROM core.ci_jobs j WHERE j.runner_id = r.id AND j.status = 'in_progress') THEN 'busy'
			     WHEN r.last_seen_at IS NOT NULL AND r.last_seen_at > $1 THEN 'online'
			     ELSE 'offline' END
		FROM core.ci_runners r ORDER BY r.name, r.id`, now.Add(-RunnerOnlineWindow))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunnerRow{}
	for rows.Next() {
		var r RunnerRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Labels, &r.Version, &r.CreatedAt, &r.LastSeenAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRunner revoca un runner: la riga sparisce, quindi il suo token non si risolve più
// e non prende altri job. I job che stava eseguendo sono chiusi come annullati.
func DeleteRunner(ctx context.Context, pool *pgxpool.Pool, now time.Time, id uuid.UUID, rep Reporter) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Stesso lock della coda: nessun job può essere preso mentre il runner sparisce.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('core.ci_queue', 0))`); err != nil {
		return err
	}
	var running []uuid.UUID
	rows, err := tx.Query(ctx, `SELECT id FROM core.ci_jobs WHERE runner_id = $1 AND status = 'in_progress'`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var j uuid.UUID
		if err := rows.Scan(&j); err != nil {
			rows.Close()
			return err
		}
		running = append(running, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM core.ci_runners WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRunnerNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// I job che il runner stava eseguendo non hanno più nessuno che li chiuda: annullati.
	for _, j := range running {
		if err := CompleteJobReported(context.WithoutCancel(ctx), pool, now, j, ConclusionCancelled, rep); err != nil {
			return fmt.Errorf("chiusura del job %s: %w", j, err)
		}
	}
	return nil
}

// ClaimExternal dà al runner il job in coda più vecchio che può eseguire: runner_type
// external, etichette di runs-on tutte fra quelle del runner, un solo job per volta
// per runner. Emette il token del job nella stessa transazione. false se non c'è nulla.
func ClaimExternal(ctx context.Context, pool *pgxpool.Pool, now time.Time, runner RunnerRow, tokenTTL time.Duration, rep Reporter) (JobRow, RunRow, string, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return JobRow{}, RunRow{}, "", false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('core.ci_queue', 0))`); err != nil {
		return JobRow{}, RunRow{}, "", false, err
	}
	// Il runner deve esistere ancora (revoca concorrente) e non avere già un job.
	var busy bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.ci_jobs WHERE runner_id = $1 AND status = 'in_progress')
		FROM core.ci_runners WHERE id = $1`, runner.ID).Scan(&busy)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRow{}, RunRow{}, "", false, ErrRunnerToken
	}
	if err != nil {
		return JobRow{}, RunRow{}, "", false, err
	}
	if busy {
		return JobRow{}, RunRow{}, "", false, nil
	}
	var jobID, runID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT j.id, j.run_id
		FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id
		WHERE j.status = 'queued' AND j.runner_type = 'external' AND r.status IN ('queued', 'in_progress')
			AND j.runs_on <@ $1::text[]
		ORDER BY j.queued_at, j.seq
		LIMIT 1
		FOR UPDATE OF j SKIP LOCKED`, runner.Labels).Scan(&jobID, &runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRow{}, RunRow{}, "", false, nil
	}
	if err != nil {
		return JobRow{}, RunRow{}, "", false, err
	}
	rid := runner.ID
	job, run, token, _, err := startClaimed(ctx, tx, now, jobID, runID, runner.Name, &rid, tokenTTL, 0, rep)
	if err != nil {
		return JobRow{}, RunRow{}, "", false, fmt.Errorf("avvio del job: %w", err)
	}
	return job, run, token, true, nil
}

// ResolveRunnerJob è l'autenticazione di UpdateTask e UpdateLog per il runner di Gitea
// (act_runner, GIT-329): quel client riusa il token del runner (gsr_) su ogni chiamata,
// non conosce il token del job. Vale solo per il job che il runner sta eseguendo (un
// runner esterno ne ha uno per volta): ErrJobToken se il token non è di un runner o il
// runner non ha un job in corso.
func ResolveRunnerJob(ctx context.Context, pool *pgxpool.Pool, now time.Time, token string) (JobAuth, error) {
	if !strings.HasPrefix(token, RunnerTokenPrefix) || len(token) > 128 {
		return JobAuth{}, ErrJobToken
	}
	var a JobAuth
	err := pool.QueryRow(ctx, `
		SELECT j.id, j.task_id, j.run_id, r.repo_id, r.sha, j.job_key
		FROM core.ci_runners rn
		JOIN core.ci_jobs j ON j.runner_id = rn.id AND j.status = 'in_progress'
		JOIN core.ci_runs r ON r.id = j.run_id
		WHERE rn.token_hash = $1
		ORDER BY j.started_at DESC NULLS LAST
		LIMIT 1`, hashJobToken(token)).Scan(&a.JobID, &a.TaskID, &a.RunID, &a.RepoID, &a.SHA, &a.JobKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobAuth{}, ErrJobToken
	}
	if err != nil {
		return JobAuth{}, err
	}
	if _, err := pool.Exec(ctx, `UPDATE core.ci_runners SET last_seen_at = $2 WHERE token_hash = $1`, hashJobToken(token), now); err != nil {
		return JobAuth{}, err
	}
	return a, nil
}
