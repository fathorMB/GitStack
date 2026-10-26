package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// Controller dei deploy delle app (v3-a, GIT-343, ADR 0002). Abilitato con
// GITSTACK_CORE_APPS_ENABLED=true; GITSTACK_CORE_APPS_DOMAIN è il dominio delle app (H2) e
// GITSTACK_CORE_APPS_AUTH_MIDDLEWARE il forward-auth del login (`nome` o `namespace/nome`): senza
// quest'ultimo le app con accesso `login` non ricevono una rotta. GITSTACK_CORE_APPS_PROGRESS_TIMEOUT
// (secondi) è il tempo massimo perché una versione superi il controllo di salute.
const (
	envAppsEnabled         = "GITSTACK_CORE_APPS_ENABLED"
	envAppsDomain          = "GITSTACK_CORE_APPS_DOMAIN"
	envAppsAuthMiddleware  = "GITSTACK_CORE_APPS_AUTH_MIDDLEWARE"
	envAppsProgressTimeout = "GITSTACK_CORE_APPS_PROGRESS_TIMEOUT"
)

// startApps avvia il controller e ritorna le opzioni del router; senza abilitazione ritorna nil.
// keys è il portachiavi dei segreti (GITSTACK_WEBHOOK_SECRET_KEY): dà i segreti da mascherare nei log
// delle app (GIT-350). Senza, le righe delle copie non si raccolgono.
func startApps(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, keys *webhooks.Keyring) ([]httpserver.Option, error) {
	domain := strings.TrimSpace(os.Getenv(envAppsDomain))
	opts := []httpserver.Option{}
	if domain != "" {
		opts = append(opts, httpserver.WithAppsDomain(domain))
	}
	if on, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(envAppsEnabled))); !on {
		return opts, nil
	}
	rc, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("configurazione Kubernetes in-cluster: %w", err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("client Kubernetes: %w", err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("client Kubernetes dinamico: %w", err)
	}
	st := store.New(pool)
	cfg := apps.Config{AppsDomain: domain, AuthMiddleware: strings.TrimSpace(os.Getenv(envAppsAuthMiddleware)), Log: logger}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envAppsProgressTimeout))); err == nil && v > 0 {
		cfg.ProgressTimeout = time.Duration(v) * time.Second
	}
	// Segreti per il mascheramento dei log (GIT-348): senza portachiavi non si legge nessun segreto,
	// quindi nessuna riga di copia si raccoglie.
	cfg.Secrets = func(ctx context.Context, appID uuid.UUID) (map[string]string, error) {
		if !keys.Enabled() {
			return map[string]string{}, nil
		}
		return st.AppSecretValues(ctx, keys, appID)
	}
	if !keys.Enabled() {
		logger.Warn("GITSTACK_WEBHOOK_SECRET_KEY non impostata: le righe delle copie delle app non si raccolgono")
	}
	ctl := apps.New(st, kube, dyn, cfg)
	go ctl.Run(ctx)
	return append(opts, httpserver.WithAppController(ctl)), nil
}
