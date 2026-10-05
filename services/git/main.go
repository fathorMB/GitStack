// Command git avvia il servizio git: configurazione da variabili d'ambiente
// (GITSTACK_GIT_*), repo bare su una directory dei dati, API interna per core
// protetta da header firmati, /healthz e /readyz, log JSON, arresto pulito su
// SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/config"
	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

func main() {
	os.Exit(run(os.Stdout))
}

func run(out io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(out, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	if lvl, ok := parseLevel(cfg.LogLevel); ok {
		logger = slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl}))
	}
	logger = logger.With("service", "git")

	store, err := repostore.New(cfg.DataDir)
	if err != nil {
		logger.Error("archivio dei repo non avviabile", "err", err)
		return 1
	}
	if cfg.ServiceSecret == "" {
		logger.Warn("segreto di servizio non configurato: /internal/* risponde sempre 401", "env", config.EnvServiceSecret)
	}
	if err := store.Ready(); err != nil {
		logger.Warn("directory dei dati non pronta: /readyz risponde 503", "dir", cfg.DataDir, "err", err)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpserver.NewRouter(httpserver.Deps{
			Store:   store,
			Content: newContent(),
			Secret:  cfg.ServiceSecret,
			Logger:  logger,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("git in ascolto", "addr", cfg.Addr, "dataDir", cfg.DataDir)
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
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			logger.Error("arresto non riuscito", "err", err)
			return 1
		}
	}
	return 0
}

func parseLevel(s string) (slog.Level, bool) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return 0, false
	}
	return l, true
}
