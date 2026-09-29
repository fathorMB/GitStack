// Package config carica la configurazione di identity da variabili
// d'ambiente: nessun file di configurazione, per restare semplice da
// distribuire (Docker, Helm chart di GIT-36). Stessa convenzione di
// core e gateway (services/core/internal/config, services/gateway/internal/config).
package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Config è la configurazione di identity, interamente da variabili
// d'ambiente.
type Config struct {
	// Addr è l'indirizzo di ascolto HTTP, es. ":8080".
	Addr string

	// DatabaseURL è la stringa di connessione Postgres (es.
	// "postgres://user:pass@host:5432/gitstack?sslmode=disable"). Nessun
	// default: obbligatoria. Lo schema dedicato ("identity", D6) è
	// applicato dalle migrazioni, non dalla stringa di connessione.
	DatabaseURL string

	// DBMaxConns è il numero massimo di connessioni nel pool verso Postgres.
	DBMaxConns int32

	// MigrationsTimeout è il tempo massimo concesso all'applicazione delle
	// migrazioni all'avvio, prima di rinunciare.
	MigrationsTimeout time.Duration

	// LogLevel è il livello minimo dei log strutturati ("debug", "info",
	// "warn", "error").
	LogLevel string

	// SessionTTL è la durata assoluta di una sessione web.
	SessionTTL time.Duration

	// TrustedProxies sono le reti (CIDR) da cui identity si fida
	// dell'header X-Gitstack-Client-Ip impostato dal gateway. Vuoto (default):
	// l'IP del client è sempre r.RemoteAddr.
	TrustedProxies []*net.IPNet
}

const (
	// EnvAddr è il nome della variabile d'ambiente per l'indirizzo di
	// ascolto HTTP (default ":8080").
	EnvAddr = "GITSTACK_IDENTITY_ADDR"

	// EnvDatabaseURL è il nome della variabile d'ambiente obbligatoria
	// per la stringa di connessione Postgres.
	EnvDatabaseURL = "GITSTACK_IDENTITY_DB_URL"

	// EnvDBMaxConns è il nome della variabile d'ambiente per il numero
	// massimo di connessioni nel pool Postgres (default 10).
	EnvDBMaxConns = "GITSTACK_IDENTITY_DB_MAX_CONNS"

	// EnvMigrationsTimeout è il nome della variabile d'ambiente per il
	// timeout massimo delle migrazioni (default 30s).
	EnvMigrationsTimeout = "GITSTACK_IDENTITY_MIGRATIONS_TIMEOUT"

	// EnvLogLevel è il nome della variabile d'ambiente per il livello
	// dei log (default "info").
	EnvLogLevel = "GITSTACK_IDENTITY_LOG_LEVEL"

	// EnvSessionTTL: durata delle sessioni web (default 168h, 7 giorni).
	EnvSessionTTL = "GITSTACK_IDENTITY_SESSION_TTL"

	// EnvTrustedProxies: elenco CIDR separati da virgola dei proxy fidati
	// (es. "10.42.0.0/16"). Default vuoto.
	EnvTrustedProxies = "GITSTACK_IDENTITY_TRUSTED_PROXIES"

	defaultSessionTTL = 7 * 24 * time.Hour

	defaultAddr              = ":8080"
	defaultDBMaxConns        = int32(10)
	defaultMigrationsTimeout = 30 * time.Second
	defaultLogLevel          = "info"
)

// Load legge la configurazione dall'ambiente di processo. Ritorna un errore
// che elenca tutto ciò che non va, così chi avvia identity lo vede in un colpo
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
		SessionTTL:        defaultSessionTTL,
	}

	if v, ok := lookup(EnvSessionTTL); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", EnvSessionTTL, v))
		} else {
			cfg.SessionTTL = d
		}
	}

	if v, ok := lookup(EnvTrustedProxies); ok && strings.TrimSpace(v) != "" {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			_, n, err := net.ParseCIDR(part)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s contiene un CIDR non valido: %q", EnvTrustedProxies, part))
				continue
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, n)
		}
	}

	if v, ok := lookup(EnvAddr); ok && strings.TrimSpace(v) != "" {
		cfg.Addr = v
	}

	dbURLRaw, ok := lookup(EnvDatabaseURL)
	if !ok || strings.TrimSpace(dbURLRaw) == "" {
		errs = append(errs, fmt.Sprintf("%s è obbligatoria (es. postgres://identity:***@postgres:5432/gitstack?sslmode=disable)", EnvDatabaseURL))
	} else {
		cfg.DatabaseURL = dbURLRaw
	}

	if v, ok := lookup(EnvDBMaxConns); ok && strings.TrimSpace(v) != "" {
		n, err := parsePositiveInt32(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s non è un intero positivo valido: %q", EnvDBMaxConns, v))
		} else {
			cfg.DBMaxConns = n
		}
	}

	if v, ok := lookup(EnvMigrationsTimeout); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", EnvMigrationsTimeout, v))
		} else {
			cfg.MigrationsTimeout = d
		}
	}

	if v, ok := lookup(EnvLogLevel); ok && strings.TrimSpace(v) != "" {
		level := strings.ToLower(strings.TrimSpace(v))
		switch level {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = level
		default:
			errs = append(errs, fmt.Sprintf("%s non è un livello valido: %q (debug|info|warn|error)", EnvLogLevel, v))
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
