//go:build integration

package migrate_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestBootstrapRole_LeastPrivilege esegue davvero
// migrations/bootstrap-role.sql (non una sua riscrittura, per non perdere
// la sincronia tra documentazione e comportamento reale) con il superuser
// del container, poi verifica che il ruolo a permessi limitati risultante
// (core_app, owner del solo schema "core", D6 [c_4df04d65b3ac4910]):
//   - possa applicare le migrazioni di core (internal/migrate.Up) e fare
//     CRUD sulla risorsa di prova;
//   - non possa creare tabelle nello schema "public" (criterio aggiuntivo
//     confermato dal CTO in revisione).
func TestBootstrapRole_LeastPrivilege(t *testing.T) {
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

	// "gitstack" è il ruolo iniziale del container (POSTGRES_USER):
	// superuser, come lo sarebbe l'operatore che esegue bootstrap-role.sql
	// prima del primo avvio di core.
	adminDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string superuser non riuscita: %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("apertura pool superuser non riuscita: %v", err)
	}
	defer adminPool.Close()
	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf("ping superuser non riuscito: %v", err)
	}

	const password = "s3cret-test-only"
	script := readBootstrapScript(t)
	// Unico adattamento allo script: la sintassi ":'core_password'" è una
	// sostituzione di variabile di psql (-v core_password=...), non SQL
	// valido quando lo script gira tramite pgx invece che tramite psql.
	script = strings.ReplaceAll(script, ":'core_password'", "'"+password+"'")

	// Nessun argomento: pgx usa il protocollo semplice, che esegue più
	// istruzioni separate da ";" in un'unica chiamata, come farebbe
	// `psql -f bootstrap-role.sql`.
	if _, err := adminPool.Exec(ctx, script); err != nil {
		t.Fatalf("esecuzione di migrations/bootstrap-role.sql non riuscita: %v", err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("host del container non riuscito: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("porta mappata del container non riuscita: %v", err)
	}
	coreAppDSN := fmt.Sprintf("postgres://core_app:%s@%s:%s/gitstack?sslmode=disable", password, host, port.Port())

	coreAppPool, err := pgxpool.New(ctx, coreAppDSN)
	if err != nil {
		t.Fatalf("apertura pool core_app non riuscita: %v", err)
	}
	defer coreAppPool.Close()
	if err := coreAppPool.Ping(ctx); err != nil {
		t.Fatalf("ping core_app non riuscito: %v", err)
	}

	// Le migrazioni con core_app: lo schema "core" esiste già (creato dal
	// bootstrap, di cui core_app è owner). EnsureSchema deve trovarlo in
	// pg_namespace e non provare a ricrearlo: core_app non ha CREATE sul
	// database "gitstack", quindi un tentativo di CREATE SCHEMA fallirebbe.
	if err := migrate.Up(ctx, coreAppPool, coreAppDSN); err != nil {
		t.Fatalf("applicazione delle migrazioni con core_app non riuscita: %v", err)
	}

	// CRUD funzionante con core_app: è owner dello schema core, quindi ha
	// già i privilegi per leggere/scrivere le proprie tabelle.
	s := store.New(coreAppPool)
	created, err := s.Create(ctx, store.NewInput{Type: "repo", Name: "least-privilege"})
	if err != nil {
		t.Fatalf("Create con core_app non riuscita: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); err != nil {
		t.Fatalf("Get con core_app non riuscita: %v", err)
	}

	// core_app non deve poter creare tabelle in "public": bootstrap-role.sql
	// revoca CREATE su "public" dal ruolo implicito PUBLIC (di cui core_app
	// fa parte come ogni ruolo), non solo da core_app.
	_, err = coreAppPool.Exec(ctx, "CREATE TABLE public.should_not_be_created (id int)")
	if err == nil {
		t.Fatal("core_app non dovrebbe poter creare tabelle in public, ma CREATE TABLE è riuscita")
	}
}

func readBootstrapScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", "bootstrap-role.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lettura di %s non riuscita: %v", path, err)
	}
	return string(data)
}
