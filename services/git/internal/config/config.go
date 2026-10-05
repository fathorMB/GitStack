// Package config carica la configurazione del servizio git da variabili
// d'ambiente, con la stessa convenzione di identity e core.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Nomi delle variabili d'ambiente.
const (
	// EnvAddr: indirizzo di ascolto HTTP (default ":8080").
	EnvAddr = "GITSTACK_GIT_ADDR"
	// EnvDataDir: directory dei repo (in produzione il PVC git-data). Obbligatoria.
	EnvDataDir = "GITSTACK_GIT_DATA_DIR"
	// EnvLogLevel: debug|info|warn|error (default info).
	EnvLogLevel = "GITSTACK_GIT_LOG_LEVEL"
	// EnvServiceSecret: segreto di servizio, lo stesso di core e identity.
	EnvServiceSecret = "GITSTACK_IDENTITY_SERVICE_SECRET"
)

// Config è la configurazione del servizio git.
type Config struct {
	Addr     string
	DataDir  string
	LogLevel string
	// ServiceSecret firma gli header d'identità; vuoto = ogni chiamata
	// interna è rifiutata con 401. Non va mai nei log né negli errori.
	ServiceSecret string
}

// Load legge la configurazione dall'ambiente di processo.
func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	var errs []string
	cfg := Config{Addr: ":8080", LogLevel: "info"}

	if v, ok := lookup(EnvAddr); ok && strings.TrimSpace(v) != "" {
		cfg.Addr = strings.TrimSpace(v)
	}
	if v, ok := lookup(EnvDataDir); !ok || strings.TrimSpace(v) == "" {
		errs = append(errs, fmt.Sprintf("%s è obbligatoria (es. /data)", EnvDataDir))
	} else {
		abs, err := filepath.Abs(strings.TrimSpace(v))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s non è un percorso valido: %q", EnvDataDir, v))
		} else {
			cfg.DataDir = abs
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
	// Il valore del segreto non entra mai in un messaggio d'errore.
	if v, ok := lookup(EnvServiceSecret); ok {
		cfg.ServiceSecret = strings.TrimSpace(v)
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("configurazione non valida:\n- %s", strings.Join(errs, "\n- "))
	}
	return cfg, nil
}
