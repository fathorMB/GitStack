package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Limiti degli outputs di un job (GIT-501): come su GitHub sono stringhe, ma qui restano
// piccole perche' viaggiano nel task dei job a valle. Quello che li supera si scarta.
const (
	MaxJobOutputs     = 50
	MaxOutputNameLen  = 128
	MaxOutputValueLen = 64 << 10
)

// SetJobOutputs unisce gli outputs riportati dal runner a quelli gia' salvati sul job (ancora in
// corso): act_runner di Gitea li manda a piu' riprese, e un invio parziale non cancella il
// precedente. Una chiave gia' presente si sovrascrive; il limite di MaxJobOutputs vale sul
// totale, quindi una chiave nuova oltre il limite, un nome o un valore fuori misura si scartano.
func SetJobOutputs(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID, outputs map[string]string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT outputs FROM core.ci_jobs WHERE id = $1 AND status = 'in_progress' FOR UPDATE`, jobID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	merged := map[string]string{}
	if err := json.Unmarshal(raw, &merged); err != nil {
		return fmt.Errorf("outputs del job %s: %w", jobID, err)
	}
	keys := make([]string, 0, len(outputs))
	for k := range outputs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // quali chiavi cadono oltre il limite non dipende dall'ordine della mappa
	for _, k := range keys {
		v := outputs[k]
		if k == "" || len(k) > MaxOutputNameLen || len(v) > MaxOutputValueLen {
			continue
		}
		if _, ok := merged[k]; !ok && len(merged) >= MaxJobOutputs {
			continue
		}
		merged[k] = v
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET outputs = $2 WHERE id = $1`, jobID, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// JobNeed e' l'esito e gli outputs di un job a monte (needs) per il task di un job a valle.
type JobNeed struct {
	Key        string
	Conclusion string
	Outputs    map[string]string
}

// JobNeeds carica gli esiti e gli outputs dei job `keys` del run. Una chiave che non c'e' si omette.
func JobNeeds(ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID, keys []string) ([]JobNeed, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `SELECT job_key, COALESCE(conclusion, ''), outputs FROM core.ci_jobs
		WHERE run_id = $1 AND job_key = ANY($2) ORDER BY seq`, runID, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobNeed
	for rows.Next() {
		var n JobNeed
		var raw []byte
		if err := rows.Scan(&n.Key, &n.Conclusion, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &n.Outputs); err != nil {
			return nil, fmt.Errorf("outputs del job %s: %w", n.Key, err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
