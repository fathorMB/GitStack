package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/config"
	"github.com/fathorMB/GitStack/services/core/internal/db"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
)

// repoPurgeInterval: ogni quanto il job cancella i repo eliminati scaduti.
const repoPurgeInterval = time.Hour

// attachmentCleanupInterval: ogni quanto si cercano gli allegati orfani scaduti.
const attachmentCleanupInterval = 15 * time.Minute

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

	// GITSTACK_GIT_URL e GITSTACK_CORE_PUBLIC_URL non impediscono l'avvio
	// (il chart le emette solo se valorizzate, git.enabled=false): senza git
	// le operazioni sui repo rispondono 503, senza PUBLIC_URL gli indirizzi di
	// clone si compongono dalla richiesta.
	var gitURL *url.URL
	if cfg.GitURL != "" {
		u, err := url.Parse(cfg.GitURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			logger.Error("configurazione non valida", "err", "GITSTACK_GIT_URL deve essere una URL assoluta (es. http://git:8080)")
			return 1
		}
		gitURL = u
	} else {
		logger.Warn("GITSTACK_GIT_URL non impostata: creare e modificare i repo risponderà 503")
	}
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			logger.Error("configurazione non valida", "err", "GITSTACK_CORE_PUBLIC_URL deve essere una URL assoluta (es. https://git.example.com)")
			return 1
		}
	} else {
		logger.Warn("GITSTACK_CORE_PUBLIC_URL non impostata: gli indirizzi di clone si compongono dall'host della richiesta (X-Forwarded-Host/Proto)")
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

	var routerOpts []httpserver.Option
	var identityPurger repopurge.Access
	var repoLookup any
	var gitAPI gitclient.Git
	if cfg.IdentityURL != "" {
		identityURL, err := url.Parse(cfg.IdentityURL)
		if err != nil || identityURL.Scheme == "" || identityURL.Host == "" {
			logger.Error("configurazione non valida", "err", "GITSTACK_IDENTITY_URL non è una URL assoluta valida")
			return 1
		}
		idc := identityclient.New(identityURL, cfg.ServiceSecret, 5*time.Second)
		identityPurger = idc
		repoLookup = idc
		routerOpts = append(routerOpts, httpserver.WithCreatorGranter(idc), httpserver.WithReadableLister(idc), httpserver.WithRepoIdentity(idc), httpserver.WithUserAccess(idc))
	} else {
		logger.Warn("GITSTACK_IDENTITY_URL non impostata: POST e GET /resources e le operazioni sui repo risponderanno 503")
	}

	if gitURL != nil {
		gitAPI = gitclient.New(gitURL, cfg.ServiceSecret, 30*time.Second)
		routerOpts = append(routerOpts, httpserver.WithGit(gitAPI))
	}
	routerOpts = append(routerOpts,
		httpserver.WithEmail(cfg.SMTP.Enabled()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: cfg.PublicURL, SSHHost: cfg.SSHHost, SSHPort: cfg.SSHPort, SSHOff: !cfg.SSHEnabled}),
	)
	var disk *attachments.Disk
	if cfg.AttachmentsDir != "" {
		disk = &attachments.Disk{Dir: cfg.AttachmentsDir}
		routerOpts = append(routerOpts, httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: disk, MaxBytes: cfg.AttachmentMaxBytes}))
	} else {
		logger.Warn("GITSTACK_CORE_ATTACHMENTS_DIR non impostata: gli allegati rispondono 503")
	}
	// Outbox degli eventi di dominio (M-06/B, GIT-130): le modifiche scrivono
	// l'evento nella propria transazione, il relay lo pubblica su NATS con
	// ack e ritenta con attesa crescente. Parte dopo le migrazioni e riprende
	// le righe rimaste pendenti da un riavvio o da un'interruzione di NATS.
	relay := &outbox.Relay{Pool: pool, Sink: publisher, Log: logger}
	if cfg.IdentityURL != "" {
		if look, ok := repoLookup.(outbox.Users); ok {
			relay.Users = look
		}
	}
	go relay.Run(ctx)

	// Motore delle notifiche in-app (M-06/E, GIT-133): legge gli eventi
	// dall'outbox, scrive core.notifications e fa la conservazione delle
	// lette (C9). Serve identity per sapere chi vede il repo (I8): senza,
	// nessuna notifica (mai una notifica a chi potrebbe non leggere il repo).
	var notifyEng *notify.Engine
	if id, ok := repoLookup.(notify.Identity); ok {
		eng := &notify.Engine{Pool: pool, Identity: id, Log: logger}
		notifyEng = eng
		// Email delle notifiche (M-06/F, C5): solo con un SMTP configurato.
		// Senza, EmailWindow resta 0: nessuna notifica entra nella coda delle
		// email e nessun invio viene tentato; restano le notifiche in-app.
		if cfg.SMTP.Enabled() {
			users, uok := repoLookup.(notify.Users)
			if !uok {
				logger.Warn("identity non supporta la ricerca degli utenti: le email non partono")
			} else {
				eng.EmailWindow = notify.DefaultEmailWindow
				go (&notify.EmailDispatcher{Pool: pool, Users: users, Mail: &mailer.Mailer{Cfg: cfg.SMTP},
					PublicURL: cfg.PublicURL, Log: logger}).Run(ctx)
				logger.Info("email delle notifiche attive", "smtp_host", cfg.SMTP.Host, "smtp_port", cfg.SMTP.Port, "security", string(cfg.SMTP.Security), "window", notify.DefaultEmailWindow.String())
			}
		} else {
			logger.Info("SMTP non configurato: solo notifiche in-app")
		}
		go eng.Run(ctx)
	} else {
		logger.Warn("identity non configurata: il motore delle notifiche non parte")
	}

	// Webhook (M-06/G, GIT-135): consegna firmata con coda persistente (la
	// tabella core.webhook_deliveries), uscita solo da pkg/egress (C8). Le
	// consegne pending sopravvivono a un riavvio: il motore le riprende.
	keys, err := webhooks.NewKeyring(cfg.WebhookSecretKeyID, cfg.WebhookSecretKey, cfg.WebhookSecretOldKeys)
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	if !keys.Enabled() {
		logger.Warn("GITSTACK_WEBHOOK_SECRET_KEY non impostata: i webhook con segreto rispondono 503")
	}
	hookHTTP, hookCheck, err := webhooks.NewEgress(cfg.EgressAllow, cfg.EgressDeny, cfg.EgressClusterCIDRs)
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	routerOpts = append(routerOpts, httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys, CheckURL: hookCheck}))
	hooks := &webhooks.Engine{Pool: pool, Keys: keys, HTTP: hookHTTP, Log: logger}
	if look, ok := repoLookup.(webhooks.Users); ok {
		hooks.Users = look
	}
	if mgr, ok := repoLookup.(webhooks.Managers); ok {
		hooks.Managers = mgr
	}
	go hooks.Run(ctx)
	if js, err := jetstream.New(nc); err != nil {
		logger.Error("apertura di JetStream per i webhook push non riuscita: i push non generano webhook", "err", err)
	} else {
		go runPushConsumer(ctx, js, gitpush.DurableWebhooks, hooks.EnqueuePush, logger)
		// Commit collegati e chiusura con fixes #n (M-06/C, GIT-131): servono i
		// permessi di identity; senza, nessun collegamento.
		if id, ok := repoLookup.(issuelinks.Identity); ok {
			linker := &issuelinks.Linker{Pool: pool, Identity: id, Log: logger}
			if notifyEng != nil {
				linker.Notifier = notifyEng
			}
			if rd, ok := gitAPI.(gitclient.Reader); ok {
				linker.Git = rd
			}
			go runPushConsumer(ctx, js, gitpush.DurableIssueLinks, linker.HandlePush, logger)
		} else {
			logger.Warn("identity non configurata: i commit non si collegano alle issues")
		}
	}

	router := httpserver.NewRouter(pool, publisher, cfg.ServiceSecret, routerOpts...)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Pulizia definitiva dei repo eliminati da più di 7 giorni (R2). Sicura con
	// più repliche (FOR UPDATE SKIP LOCKED); parte solo con git e identity.
	if gitAPI != nil && identityPurger != nil {
		job := &repopurge.Job{Store: store.New(pool), Git: gitAPI, Identity: identityPurger, Log: logger}
		if disk != nil {
			job.Attachments = disk
		}
		go job.Run(ctx, repoPurgeInterval)
	} else {
		logger.Warn("git o identity non configurati: la pulizia dei repo eliminati non parte")
	}

	// Allegati orfani (mai collegati) dopo il TTL documentato (24 ore).
	if disk != nil {
		cleaner := &attachments.Cleaner{Store: store.New(pool), Disk: disk, TTL: cfg.AttachmentOrphanTTL, Log: logger}
		go cleaner.Run(ctx, attachmentCleanupInterval)
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

// runPushConsumer tiene acceso un consumer git.push: se NATS non è ancora
// raggiungibile lo riprova ogni 5 secondi, finché ctx non finisce.
func runPushConsumer(ctx context.Context, js jetstream.JetStream, durable string, h gitpush.Handler, logger *slog.Logger) {
	for ctx.Err() == nil {
		err := gitpush.Run(ctx, js, durable, h, logger)
		if err == nil || ctx.Err() != nil {
			return
		}
		logger.Warn("consumer git.push non avviato, si riprova", "durable", durable, "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}
