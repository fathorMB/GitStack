// Command gateway è l'unico punto d'ingresso API di GitStack, dietro
// Traefik: espone /healthz, /readyz e l'API pubblica /v1/* (contratto
// api/openapi.yaml), instradata verso core. Verifica token e rate limiting
// arrivano in M-02; qui c'è solo il punto di aggancio (no-op).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
	"github.com/fathorMB/GitStack/services/gateway/internal/httpserver"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	logger = logger.With("service", "gateway")

	if lvl, ok := parseLevel(cfg.LogLevel); ok {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).With("service", "gateway")
	}

	router := httpserver.NewRouter(cfg, logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("gateway in ascolto", "addr", cfg.Addr, "core_url", cfg.CoreURL.String())
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
