package ci

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Change è una modifica di un'esecuzione come la vede il Reporter (GIT-281):
// Run è l'esecuzione dopo la modifica, con tutti i suoi job (in ordine di seq);
// Jobs sono i job cambiati; RunChanged dice se è cambiata l'esecuzione.
type Change struct {
	Run        RunRow
	RunChanged bool
	Jobs       []JobRow
}

// Reporter riceve, nella transazione della modifica, ogni cambio di stato di
// esecuzioni e job (CI6). Chi scrive lo stato del commit, la notifica o il
// webhook lo fa lì: l'evento esiste se e solo se la modifica è avvenuta. Un
// errore annulla la modifica (anche il cambio di stato del job).
type Reporter interface {
	Changed(ctx context.Context, tx pgx.Tx, c Change) error
}

// report carica dentro tx le esecuzioni e i job indicati, con lo stato dopo la
// modifica, e chiama rep una volta per esecuzione. rep nil: non fa nulla.
func report(ctx context.Context, tx pgx.Tx, rep Reporter, runIDs, jobIDs []uuid.UUID) error {
	if rep == nil || (len(runIDs) == 0 && len(jobIDs) == 0) {
		return nil
	}
	changes := map[uuid.UUID]*Change{}
	var order []uuid.UUID
	change := func(runID uuid.UUID) (*Change, error) {
		if c, ok := changes[runID]; ok {
			return c, nil
		}
		run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM core.ci_runs WHERE id = $1`, runID))
		if err != nil {
			return nil, err
		}
		jobs, err := loadRunJobsTx(ctx, tx, runID)
		if err != nil {
			return nil, err
		}
		run.Jobs = jobs
		c := &Change{Run: run}
		changes[runID] = c
		order = append(order, runID)
		return c, nil
	}
	for _, id := range runIDs {
		c, err := change(id)
		if err != nil {
			return err
		}
		c.RunChanged = true
	}
	if len(jobIDs) > 0 {
		rows, err := tx.Query(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE id = ANY($1) ORDER BY seq`, jobIDs)
		if err != nil {
			return err
		}
		var jobs []JobRow
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, j := range jobs {
			c, err := change(j.RunID)
			if err != nil {
				return err
			}
			c.Jobs = append(c.Jobs, j)
		}
	}
	for _, id := range order {
		if err := rep.Changed(ctx, tx, *changes[id]); err != nil {
			return err
		}
	}
	return nil
}

// loadRunJobsTx legge tutti i job di un'esecuzione dentro la transazione.
func loadRunJobsTx(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]JobRow, error) {
	rows, err := tx.Query(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE run_id = $1 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
