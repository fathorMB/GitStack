// Package identity gestisce utenti locali, sessioni, token con scope, chiavi
// SSH e login OIDC. L'implementazione arriva con le milestone successive a
// M-01/T-01 (vedi services/README.md).
//
// Questo file avvia il servizio identity con le sue dipendenze:
// configurazione da variabili d'ambiente (GITSTACK_IDENTITY_*),
// connessione a Postgres, migrazioni applicate all'avvio, e server HTTP
// con /healthz (liveness) e /readyz (readiness, verifica database).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/config"
	"github.com/fathorMB/GitStack/services/identity/internal/db"
	"github.com/fathorMB/GitStack/services/identity/internal/httpserver"
	"github.com/fathorMB/GitStack/services/identity/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run gestisce due modalità, entrambe richieste dal criterio di
// accettazione ("applicate all'avvio o da job dedicato"):
//   - `identity` (nessun argomento) o `identity serve`: applica le migrazioni e
//     poi avvia il server HTTP. È il comportamento di default nel container.
//   - `identity migrate up` / `identity migrate down [N]`: applica solo le
//     migrazioni (o il loro rollback, N step, default 1) ed esce, senza
//     avviare il server: per un job dedicato (es. init container o Job Helm).
func run(args []string) int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	logger = logger.With("service", "identity")
	if lvl, ok := parseLevel(cfg.LogLevel); ok {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).With("service", "identity")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Apriamo il pool Postgres (e lo verifichiamo con Ping) prima di
	// qualunque comando che lo tocchi.
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		// Non logghiamo mai "err": il DSN contiene la password e un
		// errore di pgx può includere il DSN completo (il CTO su GIT-30).
		logger.Error("connessione a Postgres non riuscita")
		return 1
	}
	defer pool.Close()

	cmd, steps, err := parseCommand(args)
	if err != nil {
		logger.Error("argomenti non validi", "err", err)
		return 1
	}

	switch cmd {
	case cmdMigrateDown:
		mctx, cancel := context.WithTimeout(ctx, cfg.MigrationsTimeout)
		defer cancel()
		if err := migrate.Down(mctx, pool, cfg.DatabaseURL, steps); err != nil {
			// Come per Up: non logghiamo mai "err" per non esporre il DSN.
			logger.Error("rollback migrazioni non riuscito")
			return 1
		}
		logger.Info("rollback migrazioni completato", "steps", steps)
		return 0

	case cmdMigrateUp:
		if err := applyMigrations(ctx, cfg, pool, logger); err != nil {
			return 1
		}
		return 0

	default: // cmdServe
		if err := applyMigrations(ctx, cfg, pool, logger); err != nil {
			return 1
		}
		return serve(ctx, cfg, pool, logger)
	}
}

// applyMigrations applica le migrazioni non ancora applicate. Idempotente:
// se lo schema è già alla versione più recente non fa nulla (vedi
// internal/migrate.Up).
func applyMigrations(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	mctx, cancel := context.WithTimeout(ctx, cfg.MigrationsTimeout)
	defer cancel()
	if err := migrate.Up(mctx, pool, cfg.DatabaseURL); err != nil {
		// Come per la connessione: non logghiamo mai "err" perché il DSN
		// potrebbe comparire nell'errore del driver di migrazione.
		logger.Error("applicazione migrazioni non riuscita")
		return err
	}
	logger.Info("migrazioni applicate")
	return nil
}

func serve(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) int {
	router := httpserver.NewRouter(pool)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("identity in ascolto", "addr", cfg.Addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("il server si è fermato per un errore", "err", err)
			return 1
		}
	case <-ctx.Done():
		logger.Info("arresto in corso")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("arresto non riuscito", "err", err)
			return 1
		}
	}

	return 0
}

func parseLevel(level string) (slog.Level, bool) {
	switch level {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

type command int

const (
	cmdServe command = iota
	cmdMigrateUp
	cmdMigrateDown
)

// parseCommand riconosce `serve` (default), `migrate up` e
// `migrate down [N]` (N step di rollback, default 1).
func parseCommand(args []string) (command, int, error) {
	if len(args) == 0 || args[0] == "serve" {
		return cmdServe, 0, nil
	}
	if args[0] != "migrate" {
		return 0, 0, fmt.Errorf("comando sconosciuto: %q (atteso: serve|migrate)", args[0])
	}
	if len(args) < 2 || args[1] == "up" {
		return cmdMigrateUp, 0, nil
	}
	if args[1] != "down" {
		return 0, 0, fmt.Errorf("sottocomando 'migrate' sconosciuto: %q (atteso: up|down)", args[1])
	}

	steps := 1
	if len(args) >= 3 {
		n, err := strconv.Atoi(args[2])
		if err != nil || n <= 0 {
			return 0, 0, fmt.Errorf("numero di step non valido per 'migrate down': %q", args[2])
		}
		steps = n
	}
	return cmdMigrateDown, steps, nil
}
