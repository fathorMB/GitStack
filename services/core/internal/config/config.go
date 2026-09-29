// Package config carica la configurazione di core da variabili d'ambiente:
// nessun file di configurazione, per restare semplice da distribuire
// (Docker, Helm chart di GIT-8). Stessa convenzione del gateway
// (services/gateway/internal/config).
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config è la configurazione di core, interamente da variabili d'ambiente.
type Config struct {
	// Addr è l'indirizzo di ascolto HTTP, es. ":8080".
	Addr string

	// DatabaseURL è la stringa di connessione Postgres (es.
	// "postgres://user:pass@host:5432/gitstack?sslmode=disable"). Nessun
	// default: obbligatoria. Lo schema dedicato ("core", D6
	// [c_4df04d65b3ac4910]) è applicato dalle migrazioni, non dalla stringa
	// di connessione: la connessione può puntare a un database condiviso
	// dall'installazione, non a un database dedicato per servizio.
	DatabaseURL string

	// DBMaxConns è il numero massimo di connessioni nel pool verso Postgres.
	DBMaxConns int32

	// MigrationsTimeout è il tempo massimo concesso all'applicazione delle
	// migrazioni all'avvio, prima di rinunciare.
	MigrationsTimeout time.Duration

	// NatsURL è l'indirizzo del bus NATS JetStream (es. "nats://nats:4222")
	// su cui core pubblica l'evento di prova tramite la libreria condivisa
	// di GIT-6 (pkg/events, internal/events.NATSPublisher). A differenza di
	// GITSTACK_CORE_DB_URL non è validata qui come obbligatoria: serve solo
	// al comando "serve" (che la richiede, vedi main.go), non a
	// "migrate up|down", che non tocca NATS. Nessun default: il chart Helm
	// di GIT-8 la imposta esplicitamente.
	NatsURL string

	// ServiceSecret è il segreto di servizio (Secret
	// `<release>-identity-service`, chiave `secret`) con cui il gateway firma
	// l'identità inoltrata a core. Come NatsURL serve solo al comando
	// "serve" (che lo richiede, vedi main.go), non a "migrate up|down". Mai
	// loggato.
	ServiceSecret string

	// LogLevel è il livello minimo dei log strutturati ("debug", "info",
	// "warn", "error").
	LogLevel string
}

const (
	envAddr              = "GITSTACK_CORE_ADDR"
	envDatabaseURL       = "GITSTACK_CORE_DB_URL"
	envDBMaxConns        = "GITSTACK_CORE_DB_MAX_CONNS"
	envMigrationsTimeout = "GITSTACK_CORE_MIGRATIONS_TIMEOUT"
	envNatsURL           = "GITSTACK_CORE_NATS_URL"
	envLogLevel          = "GITSTACK_CORE_LOG_LEVEL"
	envServiceSecret     = "GITSTACK_IDENTITY_SERVICE_SECRET"

	defaultAddr              = ":8080"
	defaultDBMaxConns        = int32(10)
	defaultMigrationsTimeout = 30 * time.Second
	defaultLogLevel          = "info"
)

// Load legge la configurazione dall'ambiente di processo. Ritorna un errore
// che elenca tutto ciò che non va, così chi avvia core lo vede in un colpo
// solo.
func Load() (Config, error) {
	return load(osLookupEnv)
}

// osLookupEnv adatta os.LookupEnv alla firma usata da load, per poter
// testare load senza toccare l'ambiente di processo reale.
func osLookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	var errs []string

	cfg := Config{
		Addr:              defaultAddr,
		DBMaxConns:        defaultDBMaxConns,
		MigrationsTimeout: defaultMigrationsTimeout,
		LogLevel:          defaultLogLevel,
	}

	if v, ok := lookup(envAddr); ok && strings.TrimSpace(v) != "" {
		cfg.Addr = v
	}

	dbURLRaw, ok := lookup(envDatabaseURL)
	if !ok || strings.TrimSpace(dbURLRaw) == "" {
		errs = append(errs, fmt.Sprintf("%s è obbligatoria (es. postgres://core:***@postgres:5432/gitstack?sslmode=disable)", envDatabaseURL))
	} else {
		cfg.DatabaseURL = dbURLRaw
	}

	if v, ok := lookup(envDBMaxConns); ok && strings.TrimSpace(v) != "" {
		n, err := parsePositiveInt32(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s non è un intero positivo valido: %q", envDBMaxConns, v))
		} else {
			cfg.DBMaxConns = n
		}
	}

	if v, ok := lookup(envMigrationsTimeout); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", envMigrationsTimeout, v))
		} else {
			cfg.MigrationsTimeout = d
		}
	}

	if v, ok := lookup(envNatsURL); ok && strings.TrimSpace(v) != "" {
		cfg.NatsURL = v
	}

	if v, ok := lookup(envServiceSecret); ok {
		cfg.ServiceSecret = strings.TrimSpace(v)
	}

	if v, ok := lookup(envLogLevel); ok && strings.TrimSpace(v) != "" {
		level := strings.ToLower(strings.TrimSpace(v))
		switch level {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = level
		default:
			errs = append(errs, fmt.Sprintf("%s non è un livello valido: %q (debug|info|warn|error)", envLogLevel, v))
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("configurazione non valida:\n- %s", strings.Join(errs, "\n- "))
	}

	return cfg, nil
}

func parsePositiveInt32(v string) (int32, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("valore non valido: %q", v)
	}
	return int32(n), nil
}
