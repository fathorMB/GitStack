package ci

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Limiti degli outputs di un job (GIT-501): come su GitHub sono stringhe, ma qui restano
// piccole perche' viaggiano nel task dei job a valle. Quello che li supera si scarta.
const (
	MaxJobOutputs     = 50
	MaxOutputNameLen  = 128
	MaxOutputValueLen = 64 << 10
)

// SetJobOutputs salva gli outputs riportati dal runner sul job (ancora in corso). Una riscrittura
// sostituisce la precedente; i nomi e i valori fuori dai limiti sono scartati.
func SetJobOutputs(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID, outputs map[string]string) error {
	kept := make(map[string]string, len(outputs))
	for k, v := range outputs {
		if k == "" || len(k) > MaxOutputNameLen || len(v) > MaxOutputValueLen || len(kept) >= MaxJobOutputs {
			continue
		}
		kept[k] = v
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `UPDATE core.ci_jobs SET outputs = $2 WHERE id = $1 AND status = 'in_progress'`, jobID, b)
	return err
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
