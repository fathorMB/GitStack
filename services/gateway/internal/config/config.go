// Package config carica la configurazione del gateway da variabili
// d'ambiente: nessun file di configurazione, per restare semplice da
// distribuire (Docker, Helm chart di GIT-8).
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config è la configurazione del gateway, interamente da variabili
// d'ambiente.
type Config struct {
	// Addr è l'indirizzo di ascolto HTTP, es. ":8080".
	Addr string

	// CoreURL è la base URL del servizio core (es. "http://core:8080"),
	// verso cui il gateway instrada le richieste /v1/*. Nessun default: il
	// chart Helm di GIT-8 la imposta esplicitamente.
	CoreURL *url.URL

	// CoreTimeout è il timeout per le richieste instradate verso core.
	CoreTimeout time.Duration

	// LogLevel è il livello minimo dei log strutturati ("debug", "info",
	// "warn", "error").
	LogLevel string
}

const (
	envAddr        = "GITSTACK_GATEWAY_ADDR"
	envCoreURL     = "GITSTACK_CORE_URL"
	envCoreTimeout = "GITSTACK_CORE_TIMEOUT"
	envLogLevel    = "GITSTACK_LOG_LEVEL"

	defaultAddr        = ":8080"
	defaultCoreTimeout = 5 * time.Second
	defaultLogLevel    = "info"
)

// Load legge la configurazione dall'ambiente di processo. Ritorna un errore
// che elenca tutto ciò che non va, così chi avvia il gateway lo vede in un
// colpo solo.
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
		Addr:        defaultAddr,
		CoreTimeout: defaultCoreTimeout,
		LogLevel:    defaultLogLevel,
	}

	if v, ok := lookup(envAddr); ok && strings.TrimSpace(v) != "" {
		cfg.Addr = v
	}

	coreURLRaw, ok := lookup(envCoreURL)
	if !ok || strings.TrimSpace(coreURLRaw) == "" {
		errs = append(errs, fmt.Sprintf("%s è obbligatoria (es. http://core:8080)", envCoreURL))
	} else {
		u, err := url.Parse(coreURLRaw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Sprintf("%s non è una URL assoluta valida: %q", envCoreURL, coreURLRaw))
		} else {
			cfg.CoreURL = u
		}
	}

	if v, ok := lookup(envCoreTimeout); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", envCoreTimeout, v))
		} else {
			cfg.CoreTimeout = d
		}
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
