// Package config carica la configurazione del gateway da variabili
// d'ambiente: nessun file di configurazione, per restare semplice da
// distribuire (Docker, Helm chart di GIT-8).
package config

import (
	"fmt"
	"net"
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

	// IdentityURL è la base URL del servizio identity (es.
	// "http://identity:8080"), verso cui il gateway instrada le rotte dei tag
	// auth, users, tokens, ssh-keys, organizations, teams e permissions del
	// contratto. Opzionale: se assente queste rotte non sono montate (404),
	// così un gateway senza identity resta avviabile.
	IdentityURL *url.URL

	// IdentityTimeout è il timeout per le richieste instradate verso identity.
	IdentityTimeout time.Duration

	// LogLevel è il livello minimo dei log strutturati ("debug", "info",
	// "warn", "error").
	LogLevel string

	// TrustedProxies sono le reti (CIDR) dei proxy davanti al gateway (es.
	// Traefik) di cui ci si fida per X-Forwarded-For. Vuoto (default):
	// nessun proxy fidato, l'IP del client è quello della connessione.
	TrustedProxies []*net.IPNet
}

const (
	envAddr        = "GITSTACK_GATEWAY_ADDR"
	envCoreURL     = "GITSTACK_CORE_URL"
	envCoreTimeout = "GITSTACK_CORE_TIMEOUT"
	envLogLevel    = "GITSTACK_LOG_LEVEL"

	// envTrustedProxies: CIDR separati da virgola (es. "10.42.0.0/16").
	envTrustedProxies = "GITSTACK_GATEWAY_TRUSTED_PROXIES"

	envIdentityURL     = "GITSTACK_IDENTITY_URL"
	envIdentityTimeout = "GITSTACK_IDENTITY_TIMEOUT"

	defaultAddr        = ":8080"
	defaultCoreTimeout = 5 * time.Second

	defaultIdentityTimeout = 5 * time.Second
	defaultLogLevel        = "info"
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

		IdentityTimeout: defaultIdentityTimeout,
		LogLevel:        defaultLogLevel,
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

	if v, ok := lookup(envTrustedProxies); ok {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			_, n, err := net.ParseCIDR(part)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s contiene un CIDR non valido: %q", envTrustedProxies, part))
				continue
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, n)
		}
	}

	if v, ok := lookup(envIdentityURL); ok && strings.TrimSpace(v) != "" {
		u, err := url.Parse(strings.TrimSpace(v))
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Sprintf("%s non è una URL assoluta valida: %q", envIdentityURL, v))
		} else {
			cfg.IdentityURL = u
		}
	}

	if v, ok := lookup(envIdentityTimeout); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", envIdentityTimeout, v))
		} else {
			cfg.IdentityTimeout = d
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
