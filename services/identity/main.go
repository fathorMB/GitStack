// Command identity avvia il servizio identity: configurazione da variabili
// d'ambiente (GITSTACK_IDENTITY_*), connessione a Postgres, migrazioni
// applicate all'avvio, server HTTP con /healthz (liveness) e /readyz
// (readiness, verifica database), arresto pulito su SIGINT/SIGTERM.
// Nessuna password o DSN compare mai nei log.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/config"
	"github.com/fathorMB/GitStack/services/identity/internal/db"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/httpserver"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/migrate"
	"github.com/fathorMB/GitStack/services/identity/internal/oidc"
	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/userkeys"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

// run gestisce due modalità, entrambe richieste dal criterio di
// accettazione ("applicate all'avvio o da job dedicato"):
//   - `identity` (nessun argomento) o `identity serve`: applica le migrazioni e
//     poi avvia il server HTTP. È il comportamento di default nel container.
//   - `identity migrate up` / `identity migrate down [N]`: applica solo le
//     migrazioni (o il loro rollback, N step, default 1) ed esce, senza
//     avviare il server: per un job dedicato (es. init container o Job Helm).
//
// Il parametro `out` è il writer usato per i log strutturati; in produzione
// è os.Stdout, nei test è un bytes.Buffer per verificare il contenuto.
func run(args []string, out io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(out, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}

	// Carica il rate limit del login dopo config.Load: le variabili
	// GITSTACK_IDENTITY_LOGIN_* sono lette da FromEnv (os.Getenv in
	// produzione). L'errore non contiene segreti.
	lcfg, err := loginlimit.FromEnv(os.Getenv)
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}

	logger = logger.With("service", "identity")
	if lvl, ok := parseLevel(cfg.LogLevel); ok {
		logger = slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl})).With("service", "identity")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Apriamo il pool Postgres (e lo verifichiamo con Ping) prima di
	// qualunque comando che lo tocchi.
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		// Logghiamo host e database (estratti da ParseConfig) per
		// aiutare il debug, senza mai loggare la password: il DSN
		// contiene credenziali e un errore pgx potrebbe includerlo.
		if pcfg, perr := pgxpool.ParseConfig(cfg.DatabaseURL); perr == nil {
			logger.Error("connessione a Postgres non riuscita",
				"host", pcfg.ConnConfig.Host,
				"port", pcfg.ConnConfig.Port,
				"database", pcfg.ConnConfig.Database)
		} else {
			logger.Error("GITSTACK_IDENTITY_DB_URL non valida")
		}
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
			// Non logghiamo mai "err" per non esporre il DSN.
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
		if err := bootstrapAdmin(ctx, cfg, users.New(pool, time.Now), logger); err != nil {
			return 1
		}
		return serve(ctx, cfg, pool, lcfg, logger)
	}
}

// applyMigrations applica le migrazioni non ancora applicate. Idempotente:
// se lo schema è già alla versione più recente non fa nulla (vedi
// internal/migrate.Up).
func applyMigrations(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	mctx, cancel := context.WithTimeout(ctx, cfg.MigrationsTimeout)
	defer cancel()
	if err := migrate.Up(mctx, pool, cfg.DatabaseURL); err != nil {
		// Logghiamo solo host e database, mai il DSN completo.
		if pcfg, perr := pgxpool.ParseConfig(cfg.DatabaseURL); perr == nil {
			logger.Error("applicazione migrazioni non riuscita",
				"host", pcfg.ConnConfig.Host,
				"database", pcfg.ConnConfig.Database)
		} else {
			logger.Error("applicazione migrazioni non riuscita")
		}
		return err
	}
	logger.Info("migrazioni applicate")
	return nil
}

// adminBootstrapper è la parte di users.Service usata dal bootstrap.
type adminBootstrapper interface {
	BootstrapAdmin(ctx context.Context, in users.BootstrapAdminInput) (bool, error)
}

// bootstrapAdmin crea l'admin iniziale se il database non ne ha nessuno e la
// password iniziale è configurata (Secret). Idempotente: con un admin già
// presente non fa nulla. Nei log compaiono solo lo username e l'esito, mai la
// password né errori che potrebbero contenerla.
func bootstrapAdmin(ctx context.Context, cfg config.Config, svc adminBootstrapper, logger *slog.Logger) error {
	if cfg.AdminPassword == "" {
		logger.Info("nessuna password iniziale dell'admin configurata: bootstrap saltato", "env", config.EnvAdminPassword)
		return nil
	}
	bctx, cancel := context.WithTimeout(ctx, cfg.MigrationsTimeout)
	defer cancel()
	created, err := svc.BootstrapAdmin(bctx, users.BootstrapAdminInput{Username: cfg.AdminUsername, Password: cfg.AdminPassword})
	if err != nil {
		var ve *users.ValidationError
		if errors.As(err, &ve) {
			// I motivi per campo non contengono la password.
			logger.Error("bootstrap dell'admin non riuscito: dati non validi", "username", cfg.AdminUsername, "err", ve)
		} else {
			logger.Error("bootstrap dell'admin non riuscito", "username", cfg.AdminUsername)
		}
		return err
	}
	if created {
		logger.Info("utente admin creato: la password va cambiata al primo accesso", "username", cfg.AdminUsername)
	} else {
		logger.Info("bootstrap dell'admin non necessario: esiste già un amministratore")
	}
	return nil
}

func serve(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, lcfg loginlimit.Config, logger *slog.Logger) int {
	// Assembla il servizio di autenticazione composto da users, sessions
	// e loginlimit; httpapi.New lo monta su un handler http.Handler.
	svc := &auth.Service{
		Users:    users.New(pool, time.Now),
		Sessions: sessions.New(pool, time.Now, cfg.SessionTTL),
		Limiter:  loginlimit.New(lcfg, time.Now),
	}
	oidcSvc, err := buildOIDC(ctx, cfg, pool, svc, logger)
	if err != nil {
		// Nessun segreto nell'errore: solo percorso del file e motivo.
		logger.Error("login OIDC non avviabile", "err", err)
		return 1
	}
	router := buildRouter(cfg, pool, svc, oidcSvc, logger)

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

// buildRouter monta il router reale del servizio: auth/users, token
// personali, chiavi SSH e /internal/* (protetto dal segreto di servizio). Il
// segreto non viene mai loggato: si segnala solo se manca.
func buildRouter(cfg config.Config, pool *pgxpool.Pool, svc *auth.Service, oidcSvc *oidc.Service, logger *slog.Logger) http.Handler {
	if cfg.ServiceSecret == "" {
		logger.Warn("segreto di servizio non configurato: /internal/* risponde sempre 401",
			"env", config.EnvServiceSecret)
	}
	api := httpapi.New(svc, logger,
		httpapi.WithTrustedProxies(cfg.TrustedProxies),
		httpapi.WithTokens(apitokens.New(pool, time.Now, cfg.TokenMaxLifetime)),
		httpapi.WithSSHKeys(userkeys.New(pool, time.Now)),
		httpapi.WithPermissions(permissions.New(pool, time.Now)),
		httpapi.WithServiceSecret(cfg.ServiceSecret),
		httpapi.WithOIDC(oidcSvc),
		httpapi.WithOrgs(orgs.New(pool, time.Now)),
	)
	return httpserver.NewRouter(pool, api)
}

// buildOIDC carica il file dei provider OIDC (se configurato), sincronizza i
// provider in identity.oidc_providers e crea il servizio. Senza file ritorna
// un servizio senza provider: elenco vuoto e start 404.
func buildOIDC(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, svc *auth.Service, logger *slog.Logger) (*oidc.Service, error) {
	store := oidc.NewPGStore(pool, svc.Users, svc.Sessions, time.Now)
	if cfg.OIDCConfigFile == "" {
		logger.Info("login OIDC spento", "env", config.EnvOIDCConfigFile)
		return oidc.New(store, oidc.Config{}, logger, time.Now)
	}
	providers, err := oidc.LoadFile(cfg.OIDCConfigFile, oidc.Options{})
	if err != nil {
		return nil, err
	}
	o, err := oidc.New(store, oidc.Config{
		Providers: providers, Key: cfg.OIDCEncKey, KeyID: cfg.OIDCEncKeyID, PublicURL: cfg.PublicURL,
	}, logger, time.Now)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithTimeout(ctx, cfg.MigrationsTimeout)
	defer cancel()
	if err := o.Sync(sctx); err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(providers))
	for _, p := range providers {
		slugs = append(slugs, p.Slug)
	}
	logger.Info("login OIDC attivo", "providers", slugs)
	return o, nil
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
