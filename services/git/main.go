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
	"path/filepath"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/config"
	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/smarthttp"
	"github.com/fathorMB/GitStack/services/git/internal/sshd"
	"github.com/fathorMB/GitStack/services/git/internal/upstream"
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
	runner, err := gitrun.New()
	if err != nil {
		logger.Error("git non trovato", "err", err)
		return 1
	}
	if cfg.ServiceSecret == "" {
		logger.Warn("segreto di servizio non configurato: /internal/* risponde sempre 401", "env", config.EnvServiceSecret)
	}
	if err := store.Ready(); err != nil {
		logger.Warn("directory dei dati non pronta: /readyz risponde 503", "dir", cfg.DataDir, "err", err)
	}

	// git.push su NATS: la connessione si riprova in background, quindi un
	// NATS giù all'avvio non impedisce il servizio.
	var events *pushevent.Notifier
	if cfg.NatsURL == "" {
		logger.Warn("git.push non verrà pubblicato: NATS non configurato", "env", config.EnvNatsURL)
	} else {
		np, err := pushevent.NewNATSPublisher(cfg.NatsURL)
		if err != nil {
			logger.Error("NATS non configurabile", "err", err)
			return 1
		}
		defer np.Close()
		events = &pushevent.Notifier{Pub: np, Git: runner, Logger: logger}
		defer func() {
			wctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if !events.Wait(wctx) {
				logger.Warn("arresto: pubblicazioni di git.push ancora in corso, interrotte")
			}
		}()
	}

	// Un solo upstream.Client e un solo Authorizer per smart HTTP e SSH.
	var (
		gitHandler http.Handler
		auth       *access.Authorizer
		keys       access.Keys
		rules      *receiverules.Rules
	)
	if cfg.IdentityURL != "" && cfg.CoreURL != "" && cfg.ServiceSecret != "" {
		up := upstream.New(cfg.IdentityURL, cfg.CoreURL, cfg.ServiceSecret, 5*time.Second)
		auth = &access.Authorizer{Identity: up, Core: up, Disk: store}
		keys = up
		rules, err = receiverules.Install(filepath.Join(cfg.DataDir, "hooks"), receiverules.Limits{MaxBlobBytes: cfg.MaxBlobBytes, WarnRepoBytes: cfg.RepoWarnBytes})
		if err != nil {
			logger.Error("regole alla ricezione del push non installabili", "err", err)
			return 1
		}
		gitHandler = &smarthttp.Handler{Auth: auth, Logger: logger, Rules: rules, Events: events}
	} else {
		logger.Warn("smart HTTP non configurato: servono identity, core e segreto di servizio", "env", []string{config.EnvIdentityURL, config.EnvCoreURL, config.EnvServiceSecret})
		gitHandler = unconfigured{}
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpserver.NewRouter(httpserver.Deps{
			Store:   store,
			Reads:   gitread.New(runner, store.Dir),
			Mirror:  &mirrorpush.Service{Run: runner, Dir: store.Dir},
			Content: newContent(),
			Secret:  cfg.ServiceSecret,
			Logger:  logger,
			Git:     gitHandler,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	sshSrv, err := startSSH(cfg, auth, keys, rules, events, logger)
	if err != nil {
		logger.Error("server SSH non avviabile", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("git in ascolto", "addr", cfg.Addr, "dataDir", cfg.DataDir)
		serveErr <- srv.ListenAndServe()
	}()
	sshErr := make(chan error, 1)
	if sshSrv != nil {
		go func() { sshErr <- sshSrv.Serve() }()
		defer func() { _ = sshSrv.Close() }()
	}
	select {
	case err := <-sshErr:
		if err != nil {
			logger.Error("il server SSH si è fermato per un errore", "err", err)
			return 1
		}
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

// unconfigured risponde 503 alle richieste git quando mancano identity o core.
type unconfigured struct{}

func (unconfigured) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "git HTTP non configurato", http.StatusServiceUnavailable)
}

// startSSH prepara e apre il server SSH integrato, con lo stesso
// access.Authorizer dello smart HTTP. nil, nil se è disattivato
// (GITSTACK_GIT_SSH_ADDR=off) o se manca la configurazione di identity e
// core (auth nil): in quel caso lo dice nel log.
func startSSH(cfg config.Config, auth *access.Authorizer, keys access.Keys, rules *receiverules.Rules, events *pushevent.Notifier, logger *slog.Logger) (*sshd.Server, error) {
	if cfg.SSHAddr == "" {
		logger.Info("server SSH disattivato", "env", config.EnvSSHAddr)
		return nil, nil
	}
	if auth == nil {
		logger.Warn("server SSH non avviato: servono identity, core e segreto di servizio",
			"env", []string{config.EnvIdentityURL, config.EnvCoreURL, config.EnvServiceSecret})
		return nil, nil
	}
	key, err := sshd.LoadOrCreateHostKey(cfg.SSHHostKeyFile)
	if err != nil {
		return nil, err
	}
	srv, err := sshd.New(sshd.Config{
		Addr:    cfg.SSHAddr,
		HostKey: key,
		Auth:    auth,
		Keys:    keys,
		Rules:   rules,
		Events:  events,
		Logger:  logger,
	})
	if err != nil {
		return nil, err
	}
	if err := srv.Listen(); err != nil {
		return nil, err
	}
	logger.Info("SSH in ascolto", "addr", srv.Addr().String(), "hostKey", key.PublicKey().Type())
	return srv, nil
}
