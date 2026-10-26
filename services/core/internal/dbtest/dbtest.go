//go:build integration

// Package dbtest fornisce ai test d'integrazione di core un Postgres reale
// avviato con testcontainers-go. Compilato solo con il tag di build
// "integration": `go test -tags=integration ./...`, così `go test ./...`
// (senza tag) resta veloce e non richiede un demone Docker.
//
// Un solo container per processo di test (avvio pigro, sync.Once): contiene un
// database template già migrato, e ogni chiamata a NewPool ne ricava un
// database dedicato (CREATE DATABASE ... TEMPLATE), rimosso a fine test. Il
// container non si ferma da codice (un processo ucciso non esegue i
// t.Cleanup): lo rimuove Ryuk, il reaper di testcontainers, che deve restare
// attivo (vedi docs/operations.md, «Pulizia dei container di test»).
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const templateDB = "gs_template"

var (
	once      sync.Once
	baseDSN   string // DSN del database di servizio "gitstack"
	startErr  error
	createMu  sync.Mutex // CREATE DATABASE da template: una alla volta
	adminPool *pgxpool.Pool
)

// server avvia (una volta per processo) il container e il template migrato.
func server() (string, error) {
	once.Do(func() {
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
			startErr = fmt.Errorf("avvio del container Postgres non riuscito (serve un demone Docker raggiungibile): %w", err)
			return
		}
		dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			startErr = fmt.Errorf("connection string del container Postgres non riuscita: %w", err)
			return
		}
		adminPool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			startErr = fmt.Errorf("apertura pool amministrativo non riuscita: %w", err)
			return
		}
		if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
			startErr = fmt.Errorf("creazione del database template non riuscita: %w", err)
			return
		}
		tplDSN, err := withDB(dsn, templateDB)
		if err != nil {
			startErr = err
			return
		}
		tpl, err := pgxpool.New(ctx, tplDSN)
		if err != nil {
			startErr = fmt.Errorf("apertura pool sul template non riuscita: %w", err)
			return
		}
		// Il template non deve avere connessioni aperte quando lo si copia.
		defer tpl.Close()
		if err := migrate.Up(ctx, tpl, tplDSN); err != nil {
			startErr = fmt.Errorf("applicazione delle migrazioni sul template non riuscita: %w", err)
			return
		}
		baseDSN = dsn
	})
	return baseDSN, startErr
}

func withDB(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("DSN non valido: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// NewPool ritorna un pool su un database dedicato con le migrazioni di core
// già applicate, più il suo DSN (utile ai test che richiamano internal/migrate
// direttamente, es. per verificarne l'idempotenza o il rollback). Il database
// e il pool sono rimossi a fine test (t.Cleanup).
func NewPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()

	base, err := server()
	if err != nil {
		t.Fatal(err)
	}

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	name := "gs_test_" + hex.EncodeToString(suffix[:])

	createMu.Lock()
	_, err = adminPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateDB))
	createMu.Unlock()
	if err != nil {
		t.Fatalf("creazione del database di test non riuscita: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("rimozione del database %s non riuscita: %v", name, err)
		}
	})

	dsn, err := withDB(base, name)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("apertura pool verso il database di test non riuscita: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping del database di test non riuscito: %v", err)
	}
	return pool, dsn
}
