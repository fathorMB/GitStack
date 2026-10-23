package ci

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobTicketTTL limita la finestra nella quale il pod puo' ritirare il suo job.
const JobTicketTTL = 10 * time.Minute

// JobTicketPrefix evita che un token del job sia accettato per FetchTask.
const JobTicketPrefix = "gsq_"

// ErrJobTicket non distingue ticket assente, scaduto, consumato o di un job chiuso.
var ErrJobTicket = errors.New("biglietto del job non valido")

func issueJobTicket(ctx context.Context, tx pgx.Tx, now time.Time, jobID uuid.UUID, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	ticket := JobTicketPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := tx.Exec(ctx, `INSERT INTO core.ci_job_tickets (job_id, ticket_hash, issued_at, expires_at)
		VALUES ($1, $2, $3, $4)`, jobID, hashJobToken(ticket), now, now.Add(ttl)); err != nil {
		return "", err
	}
	return ticket, nil
}

// ConsumeJobTicket consuma atomicamente il ticket e crea il token che il pod
// usera' per i successivi aggiornamenti. Il job deve essere ancora in corso.
func ConsumeJobTicket(ctx context.Context, pool *pgxpool.Pool, now time.Time, ticket string, tokenTTL time.Duration) (JobRow, RunRow, string, error) {
	if !strings.HasPrefix(ticket, JobTicketPrefix) || len(ticket) > 128 {
		return JobRow{}, RunRow{}, "", ErrJobTicket
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE core.ci_job_tickets t SET consumed_at = $2
		FROM core.ci_jobs j WHERE t.job_id = j.id AND t.ticket_hash = $1
		AND t.consumed_at IS NULL AND t.expires_at > $2 AND j.status = 'in_progress'
		RETURNING t.job_id`, hashJobToken(ticket), now).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRow{}, RunRow{}, "", ErrJobTicket
	}
	if err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE id = $1`, jobID))
	if err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM core.ci_runs WHERE id = $1`, job.RunID))
	if err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	token, err := issueJobToken(ctx, tx, now, job.ID, tokenTTL)
	if err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return JobRow{}, RunRow{}, "", err
	}
	return job, run, token, nil
}
