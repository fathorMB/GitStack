package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Controller dei database gestiti (v3-d, GIT-551, ADR 0003). Spento di default (DB23): il chart imposta
// GITSTACK_CORE_DATABASES_ENABLED=true con databases.enabled. Le altre variabili le imposta
// templates/core/deployment.yaml (GIT-565): ServiceAccount di core e ClusterRole modello per i RoleBinding,
// tabella major -> immagine per digest, cartella della CA intermedia.
// GITSTACK_CORE_DATABASES_COSIGN_IDENTITY_REGEXP e _ISSUER sono identita' e issuer della firma KEYLESS con cui GIT-566 firma le
// immagini (DB5, H10), come admin/internal/upgrade/verify.go; GITSTACK_CORE_DATABASES_COSIGN_BIN il binario (default `cosign`).
// Senza identita' e issuer la creazione di un database fallisce con un motivo esplicito: mai senza verifica.
const (
	envDatabasesEnabled    = "GITSTACK_CORE_DATABASES_ENABLED"
	envDatabasesSA         = "GITSTACK_CORE_DATABASES_CONTROLLER_SA"
	envDatabasesRole       = "GITSTACK_CORE_DATABASES_WORKLOAD_ROLE"
	envDatabasesImages     = "GITSTACK_CORE_DATABASES_POSTGRES_IMAGES"
	envDatabasesCADir      = "GITSTACK_CORE_DATABASES_CA_DIR"
	envDatabasesCosignID   = "GITSTACK_CORE_DATABASES_COSIGN_IDENTITY_REGEXP"
	envDatabasesCosignIss  = "GITSTACK_CORE_DATABASES_COSIGN_ISSUER"
	envDatabasesCosignBin  = "GITSTACK_CORE_DATABASES_COSIGN_BIN"
	envDatabasesMajors     = "GITSTACK_CORE_DATABASES_ALLOWED_MAJORS"
	envDatabasesProgressSe = "GITSTACK_CORE_DATABASES_PROGRESS_TIMEOUT"
	// Accesso dalla LAN (GIT-554): intervallo "da-a" (default 15432-15531), porte dell'installazione da non
	// assegnare mai (elenco separato da virgole, oltre a 22, 80, 443, 2222, 6443, 8443, 10250) e CIDR dei pod.
	envDatabasesLANPorts    = "GITSTACK_CORE_DATABASES_LAN_PORT_RANGE"
	envDatabasesLANReserved = "GITSTACK_CORE_DATABASES_LAN_RESERVED_PORTS"
	envDatabasesPodCIDR     = "GITSTACK_CORE_DATABASES_POD_CIDR"
	// GIT-553: chiave di dump (file con i 32 byte del Secret <release>-db-dump-key) e cartella databases-data.
	envDatabasesDumpKeyFile = "GITSTACK_CORE_DATABASES_DUMP_KEY_FILE"
	envDatabasesDataDir     = "GITSTACK_CORE_DATABASES_DATA_DIR"
)

// lanConfig legge intervallo, porte riservate e CIDR dei pod; un valore non valido ferma l'avvio (meglio che
// assegnare porte sbagliate).
func lanConfig(cfg *databases.Config) error {
	cfg.PodCIDR = strings.TrimSpace(os.Getenv(envDatabasesPodCIDR))
	if cfg.PodCIDR != "" {
		if _, _, err := net.ParseCIDR(cfg.PodCIDR); err != nil {
			return fmt.Errorf("%s: %w", envDatabasesPodCIDR, err)
		}
	}
	if v := strings.TrimSpace(os.Getenv(envDatabasesLANPorts)); v != "" {
		from, to, ok := strings.Cut(v, "-")
		f, ferr := strconv.Atoi(strings.TrimSpace(from))
		t, terr := strconv.Atoi(strings.TrimSpace(to))
		if !ok || ferr != nil || terr != nil {
			return fmt.Errorf("%s: atteso `da-a`, ricevuto %q", envDatabasesLANPorts, v)
		}
		if err := databases.ValidatePortRange(f, t); err != nil {
			return fmt.Errorf("%s: %w", envDatabasesLANPorts, err)
		}
		cfg.LANPortFrom, cfg.LANPortTo = f, t
	}
	for _, p := range strings.Split(os.Getenv(envDatabasesLANReserved), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s: porta non valida %q", envDatabasesLANReserved, p)
		}
		cfg.ReservedPorts = append(cfg.ReservedPorts, n)
	}
	return nil
}

// startDatabases avvia il controller e ritorna l'opzione del router; senza abilitazione ritorna nil e
// l'API risponde 422 databases_disabled.
func startDatabases(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, secrets store.AppSealer) ([]httpserver.Option, error) {
	if on, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(envDatabasesEnabled))); !on {
		return nil, nil
	}
	images, err := databases.ParseImages(os.Getenv(envDatabasesImages))
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%s vuota: nessuna immagine PostgreSQL per i database", envDatabasesImages)
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
	cfg := databases.Config{
		ControllerServiceAccount: strings.TrimSpace(os.Getenv(envDatabasesSA)),
		WorkloadRole:             strings.TrimSpace(os.Getenv(envDatabasesRole)),
		Images:                   images,
		CADir:                    strings.TrimSpace(os.Getenv(envDatabasesCADir)),
		Exec:                     &databases.SPDYExecutor{Config: rc, Client: kube},
		Dyn:                      dyn,
		Verifier:                 databases.CosignCLI{Binary: strings.TrimSpace(os.Getenv(envDatabasesCosignBin)), IdentityRegexp: strings.TrimSpace(os.Getenv(envDatabasesCosignID)), Issuer: strings.TrimSpace(os.Getenv(envDatabasesCosignIss))},
		Log:                      logger,
		// Secrets e' anche cio' che attiva l'AfterRestore predefinito (RelinkAfterRestore): dopo un ripristino i
		// collegamenti ripartono con password nuove e le app collegate sono ridistribuite (DB15, passo 4).
		Secrets: secrets,
	}
	cfg.DataDir = strings.TrimSpace(os.Getenv(envDatabasesDataDir))
	if f := strings.TrimSpace(os.Getenv(envDatabasesDumpKeyFile)); f != "" {
		key, err := os.ReadFile(f)
		if err != nil || len(key) < databases.MinDumpKeyLen {
			// Senza chiave i backup dei database non partono (mai in chiaro): l'API risponde backup_unavailable.
			logger.Error("databases: chiave di dump assente o troppo corta, i backup dei database sono disattivati", "file", f, "err", err)
		} else {
			cfg.DumpKey = key
		}
	}
	if err := lanConfig(&cfg); err != nil {
		return nil, err
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envDatabasesProgressSe))); err == nil && v > 0 {
		cfg.ProgressTimeout = time.Duration(v) * time.Second
	}
	if strings.TrimSpace(os.Getenv(envDatabasesCosignID)) == "" || strings.TrimSpace(os.Getenv(envDatabasesCosignIss)) == "" {
		logger.Error("databases: " + envDatabasesCosignID + " o " + envDatabasesCosignIss + " vuote, la creazione dei database sara' rifiutata finche' non e' impostata (DB5)")
	}
	ctl := databases.New(store.New(pool), kube, cfg)
	go ctl.Run(ctx)
	var majors []int
	for m := range images {
		majors = append(majors, m)
	}
	if v := strings.TrimSpace(os.Getenv(envDatabasesMajors)); v != "" {
		majors = majors[:0]
		for _, p := range strings.Split(v, ",") {
			if m, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				majors = append(majors, m)
			}
		}
	}
	return []httpserver.Option{httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: ctl, AllowedMajors: majors})}, nil
}
