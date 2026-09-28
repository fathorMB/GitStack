//go:build integration

// Package dbtest avvia un Postgres reale con testcontainers-go per i test
// d'integrazione di core (criterio di accettazione: "test d'integrazione
// contro un Postgres reale, testcontainers o servizio CI"). Compilato solo
// con il tag di build "integration": `go test -tags=integration ./...`,
// così `go test ./...` (senza tag) resta veloce e non richiede un demone
// Docker, per esempio in questo ambiente di sviluppo dove il demone non è
// disponibile (vedi README.md).
package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewPool avvia un container Postgres, applica le migrazioni di core
// (internal/migrate) e ritorna un pool pgx pronto all'uso più il suo DSN
// (utile ai test che richiamano internal/migrate direttamente, es. per
// verificarne l'idempotenza o il rollback). Il container e il pool sono
// chiusi automaticamente a fine test (t.Cleanup).
func NewPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("gitstack"),
		tcpostgres.WithUsername("gitstack"),
		tcpostgres.WithPassword("gitstack"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("avvio del container Postgres non riuscito (serve un demone Docker raggiungibile): %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminazione del container Postgres non riuscita: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string del container Postgres non riuscita: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("apertura pool verso il container Postgres non riuscita: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping del container Postgres non riuscito: %v", err)
	}

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("applicazione delle migrazioni non riuscita: %v", err)
	}

	return pool, dsn
}
