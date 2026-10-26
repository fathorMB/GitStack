package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// Controller dei deploy delle app (v3-a, GIT-343, ADR 0002). Abilitato con
// GITSTACK_CORE_APPS_ENABLED=true; GITSTACK_CORE_APPS_DOMAIN è il dominio delle app (H2) e
// GITSTACK_CORE_APPS_AUTH_URL l'indirizzo del forward-auth del gateway (/_appauth/verify): senza
// quest'ultimo le app con accesso `login` non ricevono una rotta. GITSTACK_CORE_APPS_PROGRESS_TIMEOUT
// (secondi) è il tempo massimo perché una versione superi il controllo di salute.
const (
	envAppsEnabled         = "GITSTACK_CORE_APPS_ENABLED"
	envAppsDomain          = "GITSTACK_CORE_APPS_DOMAIN"
	envAppsAuthURL         = "GITSTACK_CORE_APPS_AUTH_URL"
	envAppsProgressTimeout = "GITSTACK_CORE_APPS_PROGRESS_TIMEOUT"
	// ServiceAccount di core (`namespace/nome`) e ClusterRole modello dei permessi sull'app: con
	// entrambi il controller crea un RoleBinding in ogni namespace delle app (ADR 0002).
	envAppsControllerSA = "GITSTACK_CORE_APPS_CONTROLLER_SA"
	envAppsWorkloadRole = "GITSTACK_CORE_APPS_WORKLOAD_ROLE"
)

// startApps avvia il controller e ritorna le opzioni del router; senza abilitazione ritorna nil.
func startApps(ctx context.Context, pool *pgxpool.Pool, gatewaySecret string, logger *slog.Logger) ([]httpserver.Option, error) {
	domain := strings.TrimSpace(os.Getenv(envAppsDomain))
	opts := []httpserver.Option{}
	if domain != "" {
		opts = append(opts, httpserver.WithAppsDomain(domain))
	}
	if on, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(envAppsEnabled))); !on {
		// Hosting spento: l'API delle app risponde 503 «apps_unavailable» e non tocca il cluster.
		return append(opts, httpserver.WithAppsHostingOff()), nil
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
	cfg := apps.Config{AppsDomain: domain, AuthURL: strings.TrimSpace(os.Getenv(envAppsAuthURL)), AuthSecret: trust.SigningKey(gatewaySecret), Log: logger,
		ControllerServiceAccount: strings.TrimSpace(os.Getenv(envAppsControllerSA)), WorkloadRole: strings.TrimSpace(os.Getenv(envAppsWorkloadRole))}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envAppsProgressTimeout))); err == nil && v > 0 {
		cfg.ProgressTimeout = time.Duration(v) * time.Second
	}
	ctl := apps.New(store.New(pool), kube, dyn, cfg)
	go ctl.Run(ctx)
	return append(opts, httpserver.WithAppController(ctl)), nil
}
