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
	"strings"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/config"
	"github.com/fathorMB/GitStack/services/core/internal/db"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run gestisce due modalità, entrambe richieste dal criterio di
// accettazione ("applicate all'avvio o da job dedicato"):
//   - `core` (nessun argomento) o `core serve`: applica le migrazioni e poi
//     avvia il server HTTP. È il comportamento di default nel container
//     (vedi Dockerfile), pensato per M-01 dove non c'è ancora un job k8s
//     separato per le migrazioni.
//   - `core migrate up` / `core migrate down [N]`: applica solo le
//     migrazioni (o il loro rollback, N step, default 1) ed esce, senza
//     avviare il server: per un job dedicato (es. init container o Job
//     Helm di GIT-8).
func run(args []string) int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	logger = logger.With("service", "core")
	if lvl, ok := parseLevel(cfg.LogLevel); ok {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).With("service", "core")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		logger.Error("connessione a Postgres non riuscita", "err", err)
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
			logger.Error("rollback migrazioni non riuscito", "err", err)
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
		logger.Error("applicazione migrazioni non riuscita", "err", err)
		return err
	}
	logger.Info("migrazioni applicate")
	return nil
}

func serve(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) int {
	if strings.TrimSpace(cfg.NatsURL) == "" {
		logger.Error("configurazione non valida", "err", "GITSTACK_CORE_NATS_URL è obbligatoria per avviare il server (non per 'migrate up|down')")
		return 1
	}

	if cfg.ServiceSecret == "" {
		logger.Error("configurazione non valida", "err", "GITSTACK_IDENTITY_SERVICE_SECRET è obbligatoria per avviare il server (non per 'migrate up|down'): senza, core non può verificare l'identità inoltrata dal gateway")
		return 1
	}

	// NATSPublisher (internal/events, libreria condivisa di GIT-6): si
	// connette a NATS, apre il contesto JetStream e assicura lo stream del
	// dominio "core" prima che il server accetti richieste. La connessione
	// NATS resta aperta per tutta la vita del processo e si chiude allo
	// shutdown, insieme al pool Postgres.
	publisher, nc, err := events.NewNATSPublisher(ctx, cfg.NatsURL)
	if err != nil {
		logger.Error("connessione a NATS non riuscita", "err", err)
		return 1
	}
	defer nc.Close()

	router := httpserver.NewRouter(pool, publisher, cfg.ServiceSecret)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("core in ascolto", "addr", cfg.Addr)
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
