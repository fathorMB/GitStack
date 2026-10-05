// Package config carica la configurazione del servizio git da variabili
// d'ambiente, con la stessa convenzione di identity e core.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	// EnvSSHAddr: indirizzo del server SSH integrato (default ":2222", R7);
	// "off" lo disattiva. Mai la 22: l'installer non tocca l'SSH dell'host.
	EnvSSHAddr = "GITSTACK_GIT_SSH_ADDR"
	// EnvSSHHostKey: file della chiave host ed25519 (PEM); se manca viene
	// generato al primo avvio. Default <dati>/ssh/ssh_host_ed25519_key.
	EnvSSHHostKey = "GITSTACK_GIT_SSH_HOST_KEY_FILE"
	// EnvIdentityURL e EnvCoreURL: URL interni di identity e core, usati dallo
	// smart HTTP (token, permessi, risoluzione owner/repo). Se mancano, le
	// richieste git rispondono 503.
	EnvIdentityURL = "GITSTACK_IDENTITY_URL"
	// EnvMaxBlobSize: dimensione massima di un file in un push (R6). Numero di
	// byte o con suffisso KB, MB, GB (multipli di 1024); default 100MB; 0 = nessun limite.
	EnvMaxBlobSize = "GITSTACK_GIT_MAX_FILE_SIZE"
	// EnvRepoSizeWarn: oltre questa dimensione del repo il push è accettato con
	// un avviso (R6). Stesso formato; default 5GB; 0 = nessun avviso.
	EnvRepoSizeWarn = "GITSTACK_GIT_REPO_SIZE_WARN"
	EnvCoreURL     = "GITSTACK_CORE_URL"
)

// DefaultSSHAddr è la porta SSH di default (R7).
const DefaultSSHAddr = ":2222"

// Config è la configurazione del servizio git.
type Config struct {
	Addr     string
	DataDir  string
	LogLevel string
	// ServiceSecret firma gli header d'identità; vuoto = ogni chiamata
	// interna è rifiutata con 401. Non va mai nei log né negli errori.
	ServiceSecret string
	// SSHAddr è l'indirizzo di ascolto SSH; vuoto = SSH disattivato. Il server
	// SSH parte solo se ci sono anche IdentityURL, CoreURL e ServiceSecret.
	SSHAddr string
	// SSHHostKeyFile è il file della chiave host (valorizzato se c'è DataDir).
	SSHHostKeyFile string
	// IdentityURL e CoreURL: vuoti = smart HTTP non configurato.
	IdentityURL string
	CoreURL     string
	// MaxBlobBytes e RepoWarnBytes sono le soglie di R6 in byte (0 = disattivata).
	MaxBlobBytes  int64
	RepoWarnBytes int64
}

// Load legge la configurazione dall'ambiente di processo.
func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	var errs []string
	cfg := Config{Addr: ":8080", LogLevel: "info", SSHAddr: DefaultSSHAddr, MaxBlobBytes: 100 << 20, RepoWarnBytes: 5 << 30}

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

	if v, ok := lookup(EnvSSHAddr); ok && strings.TrimSpace(v) != "" {
		v = strings.TrimSpace(v)
		if strings.EqualFold(v, "off") {
			cfg.SSHAddr = ""
		} else {
			cfg.SSHAddr = v
		}
	}
	if v, ok := lookup(EnvSSHHostKey); ok && strings.TrimSpace(v) != "" {
		cfg.SSHHostKeyFile = strings.TrimSpace(v)
	} else if cfg.DataDir != "" {
		cfg.SSHHostKeyFile = filepath.Join(cfg.DataDir, "ssh", "ssh_host_ed25519_key")
	}
	if v, ok := lookup(EnvIdentityURL); ok {
		cfg.IdentityURL = strings.TrimSpace(v)
	}
	if v, ok := lookup(EnvCoreURL); ok {
		cfg.CoreURL = strings.TrimSpace(v)
	}

	for _, s := range []struct {
		env string
		dst *int64
	}{{EnvMaxBlobSize, &cfg.MaxBlobBytes}, {EnvRepoSizeWarn, &cfg.RepoWarnBytes}} {
		if v, ok := lookup(s.env); ok && strings.TrimSpace(v) != "" {
			n, err := ParseSize(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s non è una dimensione valida: %q (numero di byte o con suffisso KB, MB, GB)", s.env, v))
			} else {
				*s.dst = n
			}
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("configurazione non valida:\n- %s", strings.Join(errs, "\n- "))
	}
	return cfg, nil
}

// ParseSize legge una dimensione: byte nudi o con suffisso KB, MB, GB, TB
// (anche KiB…, senza distinguere le maiuscole; sempre multipli di 1024).
// Zero è ammesso, i negativi no.
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40}, {"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30}, {"TB", 1 << 40}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("dimensione non valida")
	}
	return n * mult, nil
}
