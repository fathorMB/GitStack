package ci

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StepReport è lo stato di un passo riportato dal runner (UpdateTask).
type StepReport struct {
	// Number parte da 1.
	Number int
	// Conclusion vuota: il passo non è finito.
	Conclusion string
	StartedAt  *time.Time
	StoppedAt  *time.Time
}

// ReportSteps registra lo stato dei passi del job (idempotente: l'ultimo stato vince,
// un passo già concluso non torna indietro). Il nome è «Passo N»: runner.v1 non lo porta.
func ReportSteps(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID, steps []StepReport) error {
	if len(steps) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, s := range steps {
		if s.Number < 1 || s.Number > 1000 {
			continue
		}
		status := StatusQueued
		var concl *string
		switch {
		case s.Conclusion != "":
			status, concl = StatusCompleted, &s.Conclusion
			if s.Conclusion == ConclusionCancelled {
				status = StatusCancelled
			}
		case s.StartedAt != nil:
			status = StatusInProgress
		}
		var completed *time.Time
		if concl != nil {
			completed = s.StoppedAt
			if completed == nil {
				now := time.Now().UTC()
				completed = &now
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO core.ci_steps (id, job_id, number, name, status, conclusion, started_at, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (job_id, number) DO UPDATE SET status = EXCLUDED.status, conclusion = EXCLUDED.conclusion,
				started_at = COALESCE(EXCLUDED.started_at, core.ci_steps.started_at), completed_at = EXCLUDED.completed_at
			WHERE core.ci_steps.status NOT IN ('completed', 'cancelled')`,
			uuid.New(), jobID, s.Number, fmt.Sprintf("Passo %d", s.Number), status, concl, s.StartedAt, completed); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
