package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
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
	envAppsEnabled          = "GITSTACK_CORE_APPS_ENABLED"
	envAppsDomain           = "GITSTACK_CORE_APPS_DOMAIN"
	envAppsAuthURL          = "GITSTACK_CORE_APPS_AUTH_URL"
	envAppsTraefikNamespace = "GITSTACK_CORE_APPS_TRAEFIK_NAMESPACE"
	envAppsProgressTimeout  = "GITSTACK_CORE_APPS_PROGRESS_TIMEOUT"
	envAppsGateImage        = "GITSTACK_CORE_APPS_GATE_IMAGE"
	// GIT-442: porta interna del gateway per le app (impostate dal chart; tutte o nessuna).
	envAppsGatewayNamespace = "GITSTACK_CORE_APPS_GATEWAY_NAMESPACE"
	envAppsGatewayService   = "GITSTACK_CORE_APPS_GATEWAY_SERVICE"
	envAppsGatewayPodLabels = "GITSTACK_CORE_APPS_GATEWAY_POD_LABELS"
	envAppsGatewayPort      = "GITSTACK_CORE_APPS_GATEWAY_INTERNAL_PORT"
	// Indirizzo delle metriche Prometheus di Traefik (entrypoint interno `metrics`, GIT-403): da lì
	// vengono richieste ed errori 5xx. Vuoto: richieste ed errori restano non disponibili.
	envAppsTraefikMetricsURL = "GITSTACK_CORE_APPS_TRAEFIK_METRICS_URL"
	// Let's Encrypt per i domini propri (v3-c, GIT-459). La configurazione ACME è una sola, quella del
	// resolver di Traefik scritto dall'installer: core ne sa il nome (RESOLVER; vuoto = spento),
	// l'email dell'installazione (EMAIL, sola lettura) e dove leggere il certificato servito (TLS_ADDR,
	// host:porta dell'entrypoint websecure di Traefik).
	envAppsACMEResolver = "GITSTACK_CORE_APPS_ACME_RESOLVER"
	envAppsACMEEmail    = "GITSTACK_CORE_APPS_ACME_EMAIL"
	envAppsACMETLSAddr  = "GITSTACK_CORE_APPS_ACME_TLS_ADDR"
	// ServiceAccount di core (`namespace/nome`) e ClusterRole modello dei permessi sull'app: con
	// entrambi il controller crea un RoleBinding in ogni namespace delle app (ADR 0002).
	envAppsControllerSA = "GITSTACK_CORE_APPS_CONTROLLER_SA"
	envAppsWorkloadRole = "GITSTACK_CORE_APPS_WORKLOAD_ROLE"
)

// startApps avvia il controller e ritorna le opzioni del router; senza abilitazione ritorna nil.
// secrets è il portachiavi dei segreti (GITSTACK_WEBHOOK_SECRET_KEY): serve a iniettare i segreti nei
// pod e a mascherarli nei log delle app (GIT-350). Senza, le righe delle copie non si raccolgono.
func startApps(ctx context.Context, pool *pgxpool.Pool, gatewaySecret string, egressCluster, egressNode, egressDeny []string, logger *slog.Logger, secrets store.AppSealer, onDeployFailed func(context.Context, store.App, store.AppVersion, string)) ([]httpserver.Option, *apps.Controller, error) {
	domain := strings.TrimSpace(os.Getenv(envAppsDomain))
	opts := []httpserver.Option{}
	// Barriere d'istanza per le destinazioni di uscita delle app (H16): le stesse di webhook e mirror.
	opts = append(opts, httpserver.WithAppEgress(httpserver.AppEgressConfig{ClusterCIDRs: egressCluster, NodeAddresses: egressNode, Deny: egressDeny, ProxyConfigured: apps.HasProxy(apps.ProxyEnvFrom(os.Getenv))}))
	if domain != "" {
		opts = append(opts, httpserver.WithAppsDomain(domain))
	}
	// Domini propri (v3-c, GIT-458): il dominio di GitStack (da GITSTACK_CORE_PUBLIC_URL) è riservato e
	// il controllo del DNS passa dalla policy di pkg/egress.
	domainCfg := apps.DomainConfig{AppsDomain: domain, Policy: apps.EgressPolicy(egressCluster, egressNode, egressDeny)}
	opts = append(opts, httpserver.WithAppDomains(httpserver.AppDomainsConfig{InstanceHost: hostOf(os.Getenv("GITSTACK_CORE_PUBLIC_URL")), Domains: domainCfg}))
	if on, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(envAppsEnabled))); !on {
		// Hosting spento: l'API delle app risponde 503 «apps_unavailable» e non tocca il cluster.
		return append(opts, httpserver.WithAppsHostingOff()), nil, nil
	}
	rc, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("configurazione Kubernetes in-cluster: %w", err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, nil, fmt.Errorf("client Kubernetes: %w", err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, nil, fmt.Errorf("client Kubernetes dinamico: %w", err)
	}
	st := store.New(pool)
	cfg := apps.Config{AppsDomain: domain, AuthURL: strings.TrimSpace(os.Getenv(envAppsAuthURL)), AuthSecret: trust.SigningKey(gatewaySecret), Secrets: secrets, Log: logger,
		ControllerServiceAccount: strings.TrimSpace(os.Getenv(envAppsControllerSA)), WorkloadRole: strings.TrimSpace(os.Getenv(envAppsWorkloadRole)),
		EgressClusterCIDRs: egressCluster, EgressNodeAddresses: egressNode, EgressDeny: egressDeny, ProxyEnv: apps.ProxyEnvFrom(os.Getenv), TraefikNamespace: strings.TrimSpace(os.Getenv(envAppsTraefikNamespace)), GateImage: strings.TrimSpace(os.Getenv(envAppsGateImage)),
		TraefikMetricsURL: strings.TrimSpace(os.Getenv(envAppsTraefikMetricsURL)),
		ACME: apps.ACMEConfig{Resolver: strings.TrimSpace(os.Getenv(envAppsACMEResolver)), Email: strings.TrimSpace(os.Getenv(envAppsACMEEmail)),
			TLSAddr: strings.TrimSpace(os.Getenv(envAppsACMETLSAddr))}}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envAppsProgressTimeout))); err == nil && v > 0 {
		cfg.ProgressTimeout = time.Duration(v) * time.Second
	}
	if cfg.GateImage == "" {
		logger.Error("apps: GITSTACK_CORE_APPS_GATE_IMAGE vuota, i deploy delle app saranno rifiutati finché non è impostata (GIT-422)")
	}
	cfg.Domains = domainCfg
	cfg.Gateway = gatewayAccessFrom(os.Getenv)
	cfg.OnDeployFailed = onDeployFailed
	cfg.PullCredentials = apps.PullCredentialsFor(trust.ParseKeys(gatewaySecret), time.Now)
	ctl := apps.New(st, kube, dyn, cfg)
	go ctl.Run(ctx)
	return append(opts, httpserver.WithAppController(ctl)), ctl, nil
}

// hostOf dà l'host (senza porta) di un indirizzo; vuoto se non è una URL con host.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// gatewayAccessFrom legge dall'ambiente la porta interna del gateway per le app (GIT-442). Tutte le
// variabili o nessuna: una configurazione incompleta vale come assente (comportamento di prima).
func gatewayAccessFrom(lookup func(string) string) *apps.GatewayAccess {
	g := &apps.GatewayAccess{
		Namespace: strings.TrimSpace(lookup(envAppsGatewayNamespace)),
		Service:   strings.TrimSpace(lookup(envAppsGatewayService)),
		PodLabels: map[string]string{},
	}
	for _, kv := range strings.Split(lookup(envAppsGatewayPodLabels), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k != "" {
			g.PodLabels[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	g.Port, _ = strconv.Atoi(strings.TrimSpace(lookup(envAppsGatewayPort)))
	if !g.Valid() {
		return nil
	}
	return g
}
