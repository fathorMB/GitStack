package httpserver

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// countingQuerier conta le query e risponde sempre con zero righe.
type countingQuerier struct{ n int }

type emptyRows struct{ pgx.Rows }

func (emptyRows) Next() bool { return false }
func (emptyRows) Close()     {}
func (emptyRows) Err() error { return nil }

func (c *countingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	c.n++
	return emptyRows{}, nil
}

func (c *countingQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	c.n++
	return nil
}

func (c *countingQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	c.n++
	return pgconn.CommandTag{}, nil
}

// Il numero di query per le decisioni di review non cresce con le candidate.
func TestPullReviewDecisions_QueryCountIndipendenteDalNumero(t *testing.T) {
	counts := map[int]int{}
	for _, size := range []int{1, 10, 500} {
		prs := make([]pullSearchRow, size)
		for i := range prs {
			prs[i] = pullSearchRow{prRow: prRow{ID: uuid.New()}, repoID: uuid.New()}
		}
		q := &countingQuerier{}
		got, err := pullReviewDecisions(context.Background(), q, prs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != size {
			t.Fatalf("size %d: %d decisioni", size, len(got))
		}
		for _, d := range got {
			if d != "none" {
				t.Fatalf("decisione = %q, atteso none", d)
			}
		}
		counts[size] = q.n
	}
	if counts[1] != counts[10] || counts[10] != counts[500] || counts[500] > 2 {
		t.Errorf("query per dimensione = %v, attese costanti (<= 2)", counts)
	}
}
