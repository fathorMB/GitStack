// Package config carica la configurazione di core da variabili d'ambiente:
// nessun file di configurazione, per restare semplice da distribuire
// (Docker, Helm chart di GIT-8). Stessa convenzione del gateway
// (services/gateway/internal/config).
package config

import (
	"fmt"
	"os"
	"strconv"
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

	// IdentityURL è la base URL di identity (GITSTACK_IDENTITY_URL, stesso
	// nome del gateway): core la usa per assegnare il grant admin a chi crea
	// una risorsa. Vuota: POST /resources risponde 503 (mai una risorsa senza
	// grant).
	IdentityURL string

	// WebhookSecretKey è la chiave (32 byte in esadecimale o base64) con cui si
	// cifrano i segreti dei webhook (AES-256-GCM, migrazione 0006);
	// WebhookSecretKeyID dice con quale chiave è cifrata una riga
	// (rotazione) e WebhookSecretOldKeys elenca le chiavi precedenti
	// (`id:chiave,id:chiave`), che servono solo a decifrare. Senza chiave i
	// webhook si gestiscono ma non accettano un segreto (422). Mai loggate.
	WebhookSecretKey     string
	WebhookSecretKeyID   string
	WebhookSecretOldKeys string

	// Egress: liste dell'amministratore per le chiamate in uscita dei webhook
	// (pkg/egress, C8; values.yaml egress.*). Voci separate da virgole.
	EgressAllow        []string
	EgressDeny         []string
	EgressClusterCIDRs []string

	// GitURL è la base URL interna del servizio git (GITSTACK_GIT_URL):
	// core la chiama per creare i repo su disco (API interna firmata).
	// Obbligatoria solo per "serve".
	GitURL string

	// PublicURL è la base HTTPS pubblica dell'installazione
	// (GITSTACK_CORE_PUBLIC_URL): da qui si compongono gli indirizzi di clone
	// HTTPS. Obbligatoria solo per "serve".
	PublicURL string

	// SSHHost è l'host dell'indirizzo di clone SSH (GITSTACK_CORE_SSH_HOST);
	// vuoto = l'host di PublicURL.
	SSHHost string

	// SSHPort è la porta SSH dell'installazione (GITSTACK_CORE_SSH_PORT,
	// default 2222, R7).
	SSHPort int

	// SSHEnabled è false quando GITSTACK_CORE_SSH_PORT vale "off" (server SSH
	// dell'installazione spento): core non pubblica gli indirizzi di clone SSH.
	SSHEnabled bool

	// AttachmentsDir è il volume degli allegati di issue e commenti
	// (GITSTACK_CORE_ATTACHMENTS_DIR, I9). Vuoto: upload e download di allegati
	// rispondono 503. Il volume è coperto dal backup (D19).
	AttachmentsDir string

	// AttachmentMaxBytes è il limite per allegato (GITSTACK_CORE_ATTACHMENTS_MAX_BYTES,
	// default 10 MiB).
	AttachmentMaxBytes int64

	// AttachmentOrphanTTL: un allegato mai collegato si elimina dopo questo
	// tempo (GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL, default 24h).
	AttachmentOrphanTTL time.Duration

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
	envIdentityURL       = "GITSTACK_IDENTITY_URL"
	envGitURL            = "GITSTACK_GIT_URL"
	envPublicURL         = "GITSTACK_CORE_PUBLIC_URL"

	envWebhookSecretKey     = "GITSTACK_WEBHOOK_SECRET_KEY"
	envWebhookSecretKeyID   = "GITSTACK_WEBHOOK_SECRET_KEY_ID"
	envWebhookSecretOldKeys = "GITSTACK_WEBHOOK_SECRET_OLD_KEYS"
	envEgressAllow          = "GITSTACK_EGRESS_ALLOW"
	envEgressDeny           = "GITSTACK_EGRESS_DENY"
	envEgressClusterCIDRs   = "GITSTACK_EGRESS_CLUSTER_CIDRS"
	envSSHHost           = "GITSTACK_CORE_SSH_HOST"
	envSSHPort           = "GITSTACK_CORE_SSH_PORT"

	envAttachmentsDir   = "GITSTACK_CORE_ATTACHMENTS_DIR"
	envAttachmentMax    = "GITSTACK_CORE_ATTACHMENTS_MAX_BYTES"
	envAttachmentOrphan = "GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL"

	defaultAttachmentMaxBytes  = int64(10 << 20)
	defaultAttachmentOrphanTTL = 24 * time.Hour

	defaultSSHPort = 2222

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

		AttachmentMaxBytes:  defaultAttachmentMaxBytes,
		AttachmentOrphanTTL: defaultAttachmentOrphanTTL,
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

	if v, ok := lookup(envIdentityURL); ok {
		cfg.IdentityURL = strings.TrimSpace(v)
	}

	cfg.SSHPort = defaultSSHPort
	cfg.SSHEnabled = true
	if v, ok := lookup(envGitURL); ok {
		cfg.GitURL = strings.TrimSpace(v)
	}
	if v, ok := lookup(envPublicURL); ok {
		cfg.PublicURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v, ok := lookup(envWebhookSecretKey); ok {
		cfg.WebhookSecretKey = strings.TrimSpace(v)
	}
	cfg.WebhookSecretKeyID = "k1"
	if v, ok := lookup(envWebhookSecretKeyID); ok && strings.TrimSpace(v) != "" {
		cfg.WebhookSecretKeyID = strings.TrimSpace(v)
	}
	if v, ok := lookup(envWebhookSecretOldKeys); ok {
		cfg.WebhookSecretOldKeys = strings.TrimSpace(v)
	}
	cfg.EgressAllow = splitList(lookup, envEgressAllow)
	cfg.EgressDeny = splitList(lookup, envEgressDeny)
	cfg.EgressClusterCIDRs = splitList(lookup, envEgressClusterCIDRs)
	if v, ok := lookup(envSSHHost); ok {
		cfg.SSHHost = strings.TrimSpace(v)
	}
	if v, ok := lookup(envSSHPort); ok && strings.TrimSpace(v) != "" {
		if strings.EqualFold(strings.TrimSpace(v), "off") {
			cfg.SSHEnabled = false
			cfg.SSHPort = 0
		} else if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Sprintf("%s non è una porta valida (1-65535) né \"off\": %q", envSSHPort, v))
		} else {
			cfg.SSHPort = n
		}
	}

	if v, ok := lookup(envAttachmentsDir); ok {
		cfg.AttachmentsDir = strings.TrimSpace(v)
	}
	if v, ok := lookup(envAttachmentMax); ok && strings.TrimSpace(v) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è un intero positivo di byte: %q", envAttachmentMax, v))
		} else {
			cfg.AttachmentMaxBytes = n
		}
	}
	if v, ok := lookup(envAttachmentOrphan); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è una durata valida: %q", envAttachmentOrphan, v))
		} else {
			cfg.AttachmentOrphanTTL = d
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

// splitList legge un elenco separato da virgole (voci vuote scartate).
func splitList(lookup func(string) (string, bool), key string) []string {
	v, ok := lookup(key)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parsePositiveInt32(v string) (int32, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("valore non valido: %q", v)
	}
	return int32(n), nil
}
