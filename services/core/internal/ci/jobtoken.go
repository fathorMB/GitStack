package ci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobMaxDuration è il tetto di durata di un job (ADR 0001 §3: activeDeadlineSeconds 3600).
const JobMaxDuration = time.Hour

// JobTokenGrace sono i minuti in più oltre il tetto di durata in cui il token vale ancora.
const JobTokenGrace = 5 * time.Minute

// JobTokenTTL è la validità di un token del job dal momento in cui il job è preso.
const JobTokenTTL = JobMaxDuration + JobTokenGrace

// JobTokenPrefix distingue il token del job a colpo d'occhio (e dai log dei segreti).
const JobTokenPrefix = "gsj_"

// ErrJobToken: token sconosciuto, scaduto, revocato o di un job che non gira più.
// Non dice quale dei casi: chi chiama non deve poterli distinguere.
var ErrJobToken = errors.New("token del job non valido")

// JobAuth è ciò che un token del job autorizza: quel job, di quell'esecuzione, di quel repo a quello sha.
type JobAuth struct {
	JobID  uuid.UUID
	TaskID int64
	RunID  uuid.UUID
	RepoID uuid.UUID
	SHA    string
	JobKey string
	// RunnerType è il runner_type del job: internal_pod o external.
	RunnerType string
}

func hashJobToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// issueJobToken emette il token del job nella transazione tx: 256 bit casuali,
// in chiaro solo nel valore di ritorno; nel database c'è lo SHA-256.
func issueJobToken(ctx context.Context, tx pgx.Tx, now time.Time, jobID uuid.UUID, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := JobTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := tx.Exec(ctx, `
		INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (job_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, issued_at = EXCLUDED.issued_at,
			expires_at = EXCLUDED.expires_at, revoked_at = NULL`,
		jobID, hashJobToken(token), now, now.Add(ttl)); err != nil {
		return "", err
	}
	return token, nil
}

func revokeJobToken(ctx context.Context, tx pgx.Tx, now time.Time, jobID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE core.ci_job_tokens SET revoked_at = $2 WHERE job_id = $1 AND revoked_at IS NULL`, jobID, now)
	return err
}

// ResolveJobToken verifica un token: deve esistere, non essere scaduto né revocato,
// e il suo job deve essere ancora in corso. Ogni altro caso è ErrJobToken.
func ResolveJobToken(ctx context.Context, pool *pgxpool.Pool, now time.Time, token string) (JobAuth, error) {
	if !strings.HasPrefix(token, JobTokenPrefix) || len(token) > 128 {
		return JobAuth{}, ErrJobToken
	}
	var a JobAuth
	err := pool.QueryRow(ctx, `
		SELECT j.id, j.task_id, j.run_id, r.repo_id, r.sha, j.job_key, j.runner_type
		FROM core.ci_job_tokens t
		JOIN core.ci_jobs j ON j.id = t.job_id
		JOIN core.ci_runs r ON r.id = j.run_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL AND t.expires_at > $2 AND j.status = 'in_progress'`,
		hashJobToken(token), now).Scan(&a.JobID, &a.TaskID, &a.RunID, &a.RepoID, &a.SHA, &a.JobKey, &a.RunnerType)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobAuth{}, ErrJobToken
	}
	if err != nil {
		return JobAuth{}, err
	}
	return a, nil
}
