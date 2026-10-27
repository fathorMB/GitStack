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

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/config"
	"github.com/fathorMB/GitStack/services/core/internal/db"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
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

// ciScheduleInterval: ogni quanto si cercano gli orari dei workflow scaduti (lo scatto minimo e' 15 minuti).
const ciScheduleInterval = time.Minute

// attachmentCleanupInterval: ogni quanto si cercano gli allegati orfani scaduti.
const attachmentCleanupInterval = 15 * time.Minute

func main() {
	// `core apps-gate` (GIT-418) è l'init container dei pod delle app: niente config né database.
	if len(os.Args) > 1 && os.Args[1] == "apps-gate" {
		os.Exit(runAppsGate())
	}
	os.Exit(run(os.Args[1:]))
}

// runAppsGate aspetta che la NetworkPolicy sia applicata al pod (apps.RunGate); esce con 1 se non lo è.
func runAppsGate() int {
	if err := apps.RunGate(context.Background(), os.Getenv, apps.GateTimeoutFromEnv(os.Getenv)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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

	if cfg.ServiceSecret == "" || cfg.GatewaySecret == "" {
		logger.Error("configurazione non valida", "err", "GITSTACK_SERVICE_SECRET_CORE e GITSTACK_SERVICE_SECRET_GATEWAY sono obbligatorie per avviare il server (non per 'migrate up|down'): senza, core non può verificare l'identità inoltrata dal gateway")
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
	// Proxy aziendale (N9): HTTP_PROXY, HTTPS_PROXY e NO_PROXY dal chart. Vale
	// per i webhook, per l'SMTP e (via il servizio git) per i mirror; la policy
	// di uscita si applica comunque alla destinazione vera.
	proxy, err := egress.ProxyFromEnv()
	if err != nil {
		logger.Error("configurazione del proxy non valida", "err", err)
		return 1
	}

	var ciLogs *ci.LogStore
	if cfg.CILogsDir != "" {
		ciLogs = &ci.LogStore{Dir: cfg.CILogsDir}
		routerOpts = append(routerOpts, httpserver.WithCILogs(ciLogs))
	} else {
		logger.Warn("GITSTACK_CORE_CI_LOGS_DIR non impostata: i log dei job rispondono 503")
	}
	// Artefatti e cache (CI5, N7): S3 se configurato, altrimenti disco.
	var ciBlobs ci.Blobs
	switch {
	case cfg.CIS3.Enabled():
		s3, err := ci.NewS3Blobs(ci.S3Config{Endpoint: cfg.CIS3.Endpoint, Region: cfg.CIS3.Region, Bucket: cfg.CIS3.Bucket,
			Prefix: cfg.CIS3.Prefix, AccessKey: cfg.CIS3.AccessKey, SecretKey: cfg.CIS3.SecretKey, SpoolDir: cfg.CIArtifactsDir})
		if err != nil {
			logger.Error("S3 per artefatti e cache non valido", "err", err)
			return 1
		}
		ciBlobs = s3
	case cfg.CIArtifactsDir != "":
		ciBlobs = ci.DiskBlobs{Dir: cfg.CIArtifactsDir}
	default:
		logger.Warn("GITSTACK_CORE_CI_ARTIFACTS_DIR e GITSTACK_CORE_CI_S3_* non impostate: artefatti e cache rispondono 503")
	}
	if ciBlobs != nil {
		routerOpts = append(routerOpts, httpserver.WithCIStorage(ciBlobs))
	}
	routerOpts = append(routerOpts, httpserver.WithCICapacity(cfg.CICapacity))
	// Pulizia periodica di log, artefatti e cache con le impostazioni dell'amministratore.
	if ciLogs != nil || ciBlobs != nil {
		go ci.RunStoragePurge(ctx, pool, ciLogs, ciBlobs, cfg.CIArtifactsDir, ci.DefaultStoragePurgeInterval, logger)
	}

	routerOpts = append(routerOpts, httpserver.WithInstanceCA(httpserver.CAConfig{Dir: cfg.CADir, Mode: cfg.CAMode}))
	if cfg.SystemInfoFile != "" {
		routerOpts = append(routerOpts, httpserver.WithSystemInfo(httpserver.SystemInfoConfig{File: cfg.SystemInfoFile}))
	}
	var disk *attachments.Disk
	attachCfg := httpserver.AttachmentsConfig{}
	if cfg.AttachmentsDir != "" {
		disk = &attachments.Disk{Dir: cfg.AttachmentsDir}
		attachCfg = httpserver.AttachmentsConfig{Disk: disk, MaxBytes: cfg.AttachmentMaxBytes,
			MaxRepoBytes: quotaArg(cfg.AttachmentRepoQuotaBytes), MaxUserBytes: quotaArg(cfg.AttachmentUserQuotaBytes)}
	} else {
		logger.Warn("GITSTACK_CORE_ATTACHMENTS_DIR non impostata: gli allegati rispondono 503")
	}
	if cfg.ReleaseAssetsDir != "" {
		attachCfg.ReleaseAssets = &attachments.Disk{Dir: cfg.ReleaseAssetsDir}
	}
	routerOpts = append(routerOpts, httpserver.WithAttachments(attachCfg))
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
				go (&notify.EmailDispatcher{Pool: pool, Users: users, Mail: &mailer.Mailer{Cfg: cfg.SMTP, Proxy: proxy},
					PublicURL: cfg.PublicURL, Log: logger}).Run(ctx)
				logger.Info("email delle notifiche attive", "smtp_host", cfg.SMTP.Host, "smtp_port", cfg.SMTP.Port, "security", string(cfg.SMTP.Security), "window", notify.DefaultEmailWindow.String())
			}
		} else {
			logger.Info("SMTP non configurato: solo notifiche in-app")
		}
		// Avvisi sulle credenziali (F13): identity scrive un evento a ogni cambio
		// di password, token o chiave SSH e per i token in scadenza; qui diventano
		// notifiche in-app e, con l'SMTP attivo, email.
		if src, cok := repoLookup.(notify.CredentialSource); cok {
			go (&notify.CredentialNotifier{Pool: pool, Source: src, Log: logger, EmailEnabled: eng.EmailWindow > 0}).Run(ctx)
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
	hookHTTP, hookCheck, err := webhooks.NewEgressForNode(cfg.EgressAllow, cfg.EgressDeny, cfg.EgressClusterCIDRs, cfg.EgressNodeAddresses, proxy)
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

	// Mirror in push (V8, GIT-179): il push lo esegue il servizio git, core ha
	// modello, coda, tentativi e la policy di uscita (C8). La credenziale usa lo
	// stesso portachiavi dei segreti dei webhook.
	mirrorEgress, err := mirrors.NewEgressWithNode(cfg.EgressAllow, cfg.EgressDeny, cfg.EgressClusterCIDRs, cfg.EgressNodeAddresses)
	if err != nil {
		logger.Error("configurazione non valida", "err", err)
		return 1
	}
	routerOpts = append(routerOpts, httpserver.WithMirrors(httpserver.MirrorConfig{Keys: keys, Egress: mirrorEgress}))
	mirrorEng := &mirrors.Engine{Pool: pool, Keys: keys, Egress: mirrorEgress, Log: logger}
	if mgr, ok := repoLookup.(webhooks.Managers); ok {
		mirrorEng.Managers = mgr
	}
	if pusher, ok := gitAPI.(gitclient.MirrorPusher); ok {
		mirrorEng.Git = pusher
		go mirrorEng.Run(ctx)
	} else {
		logger.Warn("git non configurato: i mirror in push non partono")
	}
	// Mirror in pull (M-09/A3, GIT-197): stesso portachiavi e stessa policy di
	// uscita; l'aggiornamento prudente lo esegue il servizio git.
	pullEng := &mirrors.PullEngine{Pool: pool, Keys: keys, Egress: mirrorEgress, Log: logger}
	if mgr, ok := repoLookup.(webhooks.Managers); ok {
		pullEng.Managers = mgr
	}
	if look, ok := repoLookup.(webhooks.Users); ok {
		pullEng.Users = look
	}
	if puller, ok := gitAPI.(gitclient.MirrorPuller); ok {
		pullEng.Git = puller
		go pullEng.Run(ctx)
	} else {
		logger.Warn("git non configurato: i mirror in pull non partono")
	}
	if js, err := jetstream.New(nc); err != nil {
		logger.Error("apertura di JetStream per i webhook push non riuscita: i push non generano webhook", "err", err)
	} else {
		go runPushConsumer(ctx, js, gitpush.DurableWebhooks, hooks.EnqueuePush, logger)
		go runPushConsumer(ctx, js, gitpush.DurableMirrors, mirrorEng.HandlePush, logger)
		// PR (GIT-265): nuovi commit sul branch di origine (synchronize, approvazioni
		// annullate) e conflitti da avvisare; servono identity e git.
		if gitAPI != nil && repoLookup != nil {
			go runPushConsumer(ctx, js, gitpush.DurablePullSync, httpserver.NewPullPushHandler(pool, publisher, routerOpts...), logger)
			// CI (v2-a, GIT-276): i push avviano i workflow; gli orari scaduti
			// partono da una scansione periodica. Servono le letture di git.
			go runPushConsumer(ctx, js, gitpush.DurableCI, httpserver.NewCIPushHandler(pool, publisher, routerOpts...), logger)
			go httpserver.RunCISchedules(ctx, pool, publisher, ciScheduleInterval, logger, routerOpts...)
		}
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

	appOpts, appCtl, err := startApps(ctx, pool, cfg.GatewaySecret, cfg.EgressClusterCIDRs, cfg.EgressNodeAddresses, logger, keys, newDeployFailedNotifier(pool, notifyEng, repoLookup, logger))
	if err != nil {
		logger.Error("controller delle app non avviato", "err", err)
		return 1
	}
	routerOpts = append(routerOpts, appOpts...)
	router := httpserver.NewRouter(pool, publisher, cfg.GatewaySecret, append(routerOpts, httpserver.WithGitSecret(cfg.GitSecret))...)

	// Dispatcher v2-a: core conserva coda e stati, ma e' l'unico processo con
	// RBAC per creare e osservare i pod del namespace ristretto dei job.
	if cfg.CIEnabled {
		kube, err := ci.NewKubernetesClient()
		if err != nil {
			logger.Error("dispatcher CI non avviato", "err", err)
			return 1
		}
		runnerCfg := ci.RunnerConfig{Namespace: cfg.CINamespace, Image: cfg.CIImage, GatewayURL: cfg.CIGatewayURL, GateInternalAddr: cfg.CIGateInternalAddr, ActionMirror: cfg.CIActionMirror, Capacity: cfg.CICapacity,
			ProxyEnv: ci.ProxyEnvFrom(os.Getenv), CAConfigMap: os.Getenv("GITSTACK_CORE_CI_CA_CONFIGMAP"),
			BuildNamespace: cfg.CIBuildNamespace, BuildImage: cfg.CIBuildImage, RegistryAddr: cfg.CIRegistryAddr}
		if runnerCfg.Image == "" || runnerCfg.GatewayURL == "" {
			logger.Error("dispatcher CI non avviato", "err", "GITSTACK_CORE_CI_IMAGE e GITSTACK_CORE_CI_GATEWAY_URL obbligatorie")
			return 1
		}
		reporter := httpserver.NewCIReporter(pool, routerOpts...)
		// Reconcile gira una sola volta: per i pod vivi riattacca un observer;
		// rieseguirlo a ogni tick ne creerebbe piu' di uno sullo stesso pod.
		if err := ci.ReconcileNS(ctx, pool, kube, runnerCfg.Namespace, runnerCfg.BuildNamespace, nil, time.Now().UTC(), reporter); err != nil {
			logger.Error("riconciliazione CI non riuscita", "err", err)
		}
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				_, _ = ci.DispatchPods(ctx, pool, kube, runnerCfg, time.Now().UTC(), reporter)
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
	}

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
		if appCtl != nil {
			job.Apps = appCtl // H5: allo scadere dei 7 giorni app e volumi spariscono dal cluster
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

	// ci-gateway (v2-a, GIT-316): porta dedicata, l'unico endpoint che il pod di un
	// job raggiunge (ADR 0001 §3). Parte solo con un indirizzo, un segreto per i
	// runner interni, i log e le letture di git.
	var ciSrv *http.Server
	if cfg.CIGatewayAddr != "" {
		rd, rok := gitAPI.(gitclient.Reader)
		switch {
		case ciLogs == nil || !rok:
			logger.Warn("log dei job o git non configurati: il ci-gateway non parte")
		default:
			ciSrv = &http.Server{
				Addr: cfg.CIGatewayAddr,
				Handler: httpserver.NewCIGateway(pool, rd, httpserver.CIGatewayConfig{
					ArchiveURL: cfg.CIGatewayURL, Logs: ciLogs, Blobs: ciBlobs, SpoolDir: cfg.CIArtifactsDir, Log: logger, Keys: keys,
					Options: routerOpts,
				}),
				ReadHeaderTimeout: 5 * time.Second,
			}
		}
	}

	// Porta esterna del ci-gateway (GIT-329): stesso gateway, ma FetchTask vale solo con il
	// token di un runner registrato. Parte solo se c'e' anche la porta interna.
	var ciExtSrv *http.Server
	if cfg.CIExternalAddr != "" && ciSrv != nil {
		rd, _ := gitAPI.(gitclient.Reader)
		ciExtSrv = &http.Server{
			Addr: cfg.CIExternalAddr,
			Handler: httpserver.NewCIGateway(pool, rd, httpserver.CIGatewayConfig{
				ArchiveURL: cfg.CIExternalURL, Logs: ciLogs, Blobs: ciBlobs, SpoolDir: cfg.CIArtifactsDir, Log: logger, Keys: keys,
				Options: routerOpts, ExternalOnly: true,
			}),
			ReadHeaderTimeout: 5 * time.Second,
		}
	}

	serveErr := make(chan error, 3)
	go func() {
		logger.Info("core in ascolto", "addr", cfg.Addr)
		serveErr <- srv.ListenAndServe()
	}()
	if ciSrv != nil {
		go func() {
			logger.Info("ci-gateway in ascolto", "addr", ciSrv.Addr)
			serveErr <- ciSrv.ListenAndServe()
		}()
	}

	if ciExtSrv != nil {
		go func() {
			logger.Info("ci-gateway esterno in ascolto", "addr", ciExtSrv.Addr)
			serveErr <- ciExtSrv.ListenAndServe()
		}()
	}

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
		if ciSrv != nil {
			_ = ciSrv.Shutdown(shutdownCtx)
		}
		if ciExtSrv != nil {
			_ = ciExtSrv.Shutdown(shutdownCtx)
		}
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

// quotaArg traduce una quota di configurazione (0 = nessun limite) in quella
// di httpserver.AttachmentsConfig (< 0 = nessun limite, 0 = default).
func quotaArg(n int64) int64 {
	if n == 0 {
		return -1
	}
	return n
}
