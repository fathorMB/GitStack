// Package db apre e verifica la connessione Postgres di identity, tramite un
// pool pgx condiviso da migrazioni, health/readiness probe.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open apre un pool di connessioni verso Postgres e verifica subito che sia
// raggiungibile (Ping), così un errore di configurazione (URL sbagliata,
// database non raggiungibile) emerge all'avvio invece che alla prima
// richiesta.
func Open(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configurazione pool Postgres non valida: %w", err)
	}
	if maxConns > 0 {
		poolCfg.MaxConns = maxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("apertura pool Postgres non riuscita: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connessione a Postgres non riuscita: %w", err)
	}

	return pool, nil
}

// Ping verifica che il database sia raggiungibile ora: usato dalla
// readiness probe (GET /readyz), che riflette lo stato reale della dipendenza.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}
