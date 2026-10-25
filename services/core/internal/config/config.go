// Package config carica la configurazione di core da variabili d'ambiente:
// nessun file di configurazione, per restare semplice da distribuire
// (Docker, Helm chart di GIT-8). Stessa convenzione del gateway
// (services/gateway/internal/config).
package config

import (
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/mailer"
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

	// ServiceSecret è il segreto di servizio di core
	// (GITSTACK_SERVICE_SECRET_CORE, Secret `<release>-service-secrets`,
	// chiave `core`): core lo usa come Bearer verso identity e per firmare le
	// chiamate al servizio git. Come NatsURL serve solo al comando "serve"
	// (che lo richiede, vedi main.go), non a "migrate up|down". Mai loggato.
	// Un elenco "nuova,vecchia" è accettato (rotazione): firma con la prima.
	ServiceSecret string

	// GatewaySecret è il segreto con cui il gateway firma l'identità
	// inoltrata a core (GITSTACK_SERVICE_SECRET_GATEWAY, chiave `gateway`).
	// Obbligatorio per "serve".
	GatewaySecret string

	// GitSecret è il segreto del servizio git (GITSTACK_SERVICE_SECRET_GIT,
	// chiave `git`): core accetta un'identità firmata da git solo per
	// risolvere owner/repo (GET /repos/{owner}/{repo}). Vuoto: nessuna
	// chiamata da git è accettata.
	GitSecret string

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
	// EgressNodeAddresses: indirizzi del nodo, sempre bloccati (F7,
	// GITSTACK_EGRESS_NODE_ADDRESSES + GITSTACK_NODE_IP da status.hostIP).
	EgressNodeAddresses []string

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
	AttachmentsDir   string
	ReleaseAssetsDir string
	// CIGatewayAddr è l'indirizzo della porta dedicata del ci-gateway
	// (GITSTACK_CORE_CI_GATEWAY_ADDR, es. ":8081", ADR 0001 §3). Vuoto: il
	// gateway non parte e nessun job può essere preso da un runner.
	CIGatewayAddr string
	// CIGatewayURL è l'indirizzo con cui il pod del job raggiunge il gateway
	// (GITSTACK_CORE_CI_GATEWAY_URL), scritto nel contesto del task.
	CIGatewayURL string
	// CIExternalAddr e CIExternalURL (GITSTACK_CORE_CI_EXTERNAL_ADDR / _URL, GIT-329): seconda
	// porta del ci-gateway per i runner esterni, esposta fuori dal cluster; accetta solo il token
	// di un runner registrato, mai il biglietto di un pod. URL: l'indirizzo pubblico, scritto nel
	// contesto dei task presi da un runner esterno. Vuoto: nessuna porta esterna.
	CIExternalAddr string
	CIExternalURL  string
	// CIGateInternalAddr è un endpoint interno che il gate deve vedere bloccato
	// prima di avviare il job (GITSTACK_CORE_CI_GATE_INTERNAL_ADDR).
	CIGateInternalAddr string
	// CIRunnerSecrets sono i segreti dei runner interni accettati da FetchTask
	// (GITSTACK_CORE_CI_RUNNER_SECRET, "nuova,vecchia" come i segreti di servizio).
	CIRunnerSecrets []string
	// CIEnabled abilita il dispatcher Kubernetes dei job interni.
	CIEnabled      bool
	CINamespace    string
	CIImage        string
	CIActionMirror string
	CICapacity     int
	// CILogsDir è la cartella dei log dei job, nel volume di core e nel
	// backup (GITSTACK_CORE_CI_LOGS_DIR, CI5). Vuoto: i log dei job rispondono 503.
	CILogsDir string
	// CIArtifactsDir è la cartella di artefatti e cache dei job su disco (CI5), nel volume di
	// core e fuori dai backup. Vuota e senza CIS3Bucket: artefatti e cache rispondono 503.
	CIArtifactsDir string
	// CIS3 sceglie S3 al posto del disco per artefatti e cache (N7): GITSTACK_CORE_CI_S3_*.
	CIS3 CIS3Config
	// SystemInfoFile è system.json, il ConfigMap system-info montato come
	// volume (GITSTACK_SYSTEM_INFO_FILE, GIT-213): lo legge GET /admin/system.
	SystemInfoFile string

	// AttachmentMaxBytes è il limite per allegato (GITSTACK_CORE_ATTACHMENTS_MAX_BYTES,
	// default 10 MiB).
	AttachmentMaxBytes int64

	// AttachmentRepoQuotaBytes e AttachmentUserQuotaBytes: byte di allegati per
	// repo e per utente (GITSTACK_CORE_ATTACHMENTS_REPO_QUOTA_BYTES, _USER_QUOTA_BYTES;
	// default 1 GiB e 512 MiB; 0 = nessun limite, F17). Valori negativi di
	// httpserver.AttachmentsConfig: qui 0 diventa -1.
	AttachmentRepoQuotaBytes int64
	AttachmentUserQuotaBytes int64

	// AttachmentOrphanTTL: un allegato mai collegato si elimina dopo questo
	// tempo (GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL, default 24h).
	AttachmentOrphanTTL time.Duration

	// SMTP è la configurazione delle email di notifica (M-06/F, C5): tutte le
	// variabili GITSTACK_CORE_SMTP_*. Facoltativa: con SMTP.Host vuoto (default)
	// GitStack manda solo notifiche in-app e non tenta nessun invio.
	SMTP mailer.Config

	// LogLevel è il livello minimo dei log strutturati ("debug", "info",
	// "warn", "error").
	LogLevel string
}

const (
	envAddr                 = "GITSTACK_CORE_ADDR"
	envDatabaseURL          = "GITSTACK_CORE_DB_URL"
	envDBMaxConns           = "GITSTACK_CORE_DB_MAX_CONNS"
	envMigrationsTimeout    = "GITSTACK_CORE_MIGRATIONS_TIMEOUT"
	envNatsURL              = "GITSTACK_CORE_NATS_URL"
	envLogLevel             = "GITSTACK_CORE_LOG_LEVEL"
	envServiceSecret        = "GITSTACK_IDENTITY_SERVICE_SECRET"
	envSecretCore           = "GITSTACK_SERVICE_SECRET_CORE"
	envSecretGateway        = "GITSTACK_SERVICE_SECRET_GATEWAY"
	envSecretGit            = "GITSTACK_SERVICE_SECRET_GIT"
	envIdentityURL          = "GITSTACK_IDENTITY_URL"
	envGitURL               = "GITSTACK_GIT_URL"
	envPublicURL            = "GITSTACK_CORE_PUBLIC_URL"
	envSSHHost              = "GITSTACK_CORE_SSH_HOST"
	envSSHPort              = "GITSTACK_CORE_SSH_PORT"
	envSMTPHost             = "GITSTACK_CORE_SMTP_HOST"
	envSMTPPort             = "GITSTACK_CORE_SMTP_PORT"
	envSMTPSecurity         = "GITSTACK_CORE_SMTP_SECURITY"
	envSMTPUser             = "GITSTACK_CORE_SMTP_USER"
	envSMTPPassword         = "GITSTACK_CORE_SMTP_PASSWORD"
	envWebhookSecretKey     = "GITSTACK_WEBHOOK_SECRET_KEY"
	envWebhookSecretKeyID   = "GITSTACK_WEBHOOK_SECRET_KEY_ID"
	envWebhookSecretOldKeys = "GITSTACK_WEBHOOK_SECRET_OLD_KEYS"
	envEgressAllow          = "GITSTACK_EGRESS_ALLOW"
	envEgressDeny           = "GITSTACK_EGRESS_DENY"
	envEgressClusterCIDRs   = "GITSTACK_EGRESS_CLUSTER_CIDRS"
	envEgressNodeAddresses  = "GITSTACK_EGRESS_NODE_ADDRESSES"
	envNodeIP               = "GITSTACK_NODE_IP"
	envSMTPFrom             = "GITSTACK_CORE_SMTP_FROM"

	envAttachmentsDir     = "GITSTACK_CORE_ATTACHMENTS_DIR"
	envCIGatewayAddr      = "GITSTACK_CORE_CI_GATEWAY_ADDR"
	envCIGatewayURL       = "GITSTACK_CORE_CI_GATEWAY_URL"
	envCIExternalAddr     = "GITSTACK_CORE_CI_EXTERNAL_ADDR"
	envCIExternalURL      = "GITSTACK_CORE_CI_EXTERNAL_URL"
	envCIGateInternalAddr = "GITSTACK_CORE_CI_GATE_INTERNAL_ADDR"
	envCIRunnerSecret     = "GITSTACK_CORE_CI_RUNNER_SECRET"
	envCIEnabled          = "GITSTACK_CORE_CI_ENABLED"
	envCINamespace        = "GITSTACK_CORE_CI_NAMESPACE"
	envCIImage            = "GITSTACK_CORE_CI_IMAGE"
	envCIActionMirror     = "GITSTACK_CORE_CI_ACTION_MIRROR"
	envCICapacity         = "GITSTACK_CORE_CI_CAPACITY"
	envCILogsDir          = "GITSTACK_CORE_CI_LOGS_DIR"
	envCIArtifactsDir     = "GITSTACK_CORE_CI_ARTIFACTS_DIR"
	envCIS3Endpoint       = "GITSTACK_CORE_CI_S3_ENDPOINT"
	envCIS3Bucket         = "GITSTACK_CORE_CI_S3_BUCKET"
	envCIS3Region         = "GITSTACK_CORE_CI_S3_REGION"
	envCIS3Prefix         = "GITSTACK_CORE_CI_S3_PREFIX"
	envCIS3AccessKey      = "GITSTACK_CORE_CI_S3_ACCESS_KEY"
	envCIS3SecretKey      = "GITSTACK_CORE_CI_S3_SECRET_KEY"
	envReleaseAssetsDir   = "GITSTACK_CORE_RELEASE_ASSETS_DIR"
	envSystemInfoFile     = "GITSTACK_SYSTEM_INFO_FILE"
	envAttachmentMax      = "GITSTACK_CORE_ATTACHMENTS_MAX_BYTES"
	envAttachRepoQuota    = "GITSTACK_CORE_ATTACHMENTS_REPO_QUOTA_BYTES"
	envAttachUserQuota    = "GITSTACK_CORE_ATTACHMENTS_USER_QUOTA_BYTES"
	envAttachmentOrphan   = "GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL"

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

		AttachmentMaxBytes:       defaultAttachmentMaxBytes,
		AttachmentRepoQuotaBytes: 1 << 30,
		AttachmentUserQuotaBytes: 512 << 20,
		AttachmentOrphanTTL:      defaultAttachmentOrphanTTL,
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

	// F9: un segreto per chiamante. Il segreto condiviso storico vale solo
	// per quelli che mancano (installazioni non gestite dal chart).
	legacy := ""
	if v, ok := lookup(envServiceSecret); ok {
		legacy = strings.TrimSpace(v)
	}
	pick := func(env string) string {
		if v, ok := lookup(env); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return legacy
	}
	cfg.ServiceSecret = pick(envSecretCore)
	cfg.GatewaySecret = pick(envSecretGateway)
	cfg.GitSecret = pick(envSecretGit)

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
	cfg.EgressNodeAddresses = append(splitList(lookup, envEgressNodeAddresses), splitList(lookup, envNodeIP)...)
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
	if v, ok := lookup(envCIGatewayAddr); ok {
		cfg.CIGatewayAddr = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIGatewayURL); ok {
		cfg.CIGatewayURL = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIExternalAddr); ok {
		cfg.CIExternalAddr = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIExternalURL); ok {
		cfg.CIExternalURL = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIGateInternalAddr); ok {
		cfg.CIGateInternalAddr = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIRunnerSecret); ok {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				cfg.CIRunnerSecrets = append(cfg.CIRunnerSecrets, s)
			}
		}
	}
	if v, ok := lookup(envCIEnabled); ok {
		cfg.CIEnabled, _ = strconv.ParseBool(strings.TrimSpace(v))
	}
	if v, ok := lookup(envCINamespace); ok {
		cfg.CINamespace = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIImage); ok {
		cfg.CIImage = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIActionMirror); ok {
		cfg.CIActionMirror = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCICapacity); ok && strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 1 || n > 3 {
			errs = append(errs, fmt.Sprintf("%s deve essere fra 1 e 3: %q", envCICapacity, v))
		} else {
			cfg.CICapacity = n
		}
	}
	if v, ok := lookup(envCILogsDir); ok {
		cfg.CILogsDir = strings.TrimSpace(v)
	}
	if v, ok := lookup(envCIArtifactsDir); ok {
		cfg.CIArtifactsDir = strings.TrimSpace(v)
	}
	errs = loadCIS3(lookup, &cfg, errs)
	if v, ok := lookup(envReleaseAssetsDir); ok {
		cfg.ReleaseAssetsDir = strings.TrimSpace(v)
	}
	if v, ok := lookup(envSystemInfoFile); ok {
		cfg.SystemInfoFile = strings.TrimSpace(v)
	}
	if v, ok := lookup(envAttachmentMax); ok && strings.TrimSpace(v) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Sprintf("%s non è un intero positivo di byte: %q", envAttachmentMax, v))
		} else {
			cfg.AttachmentMaxBytes = n
		}
	}
	for _, q := range []struct {
		env string
		dst *int64
	}{{envAttachRepoQuota, &cfg.AttachmentRepoQuotaBytes}, {envAttachUserQuota, &cfg.AttachmentUserQuotaBytes}} {
		if v, ok := lookup(q.env); ok && strings.TrimSpace(v) != "" {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil || n < 0 {
				errs = append(errs, fmt.Sprintf("%s non è un intero di byte (0 = nessun limite): %q", q.env, v))
			} else {
				*q.dst = n
			}
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

	errs = loadSMTP(lookup, &cfg, errs)

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

// CIS3Config è lo storage S3 di artefatti e cache. Bucket vuoto = disco.
type CIS3Config struct {
	Endpoint, Region, Bucket, Prefix, AccessKey, SecretKey string
}

// Enabled: S3 è configurato.
func (c CIS3Config) Enabled() bool { return c.Bucket != "" }

// loadCIS3 legge GITSTACK_CORE_CI_S3_*: con il bucket servono anche endpoint e chiavi.
func loadCIS3(lookup func(string) (string, bool), cfg *Config, errs []string) []string {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	c := CIS3Config{Endpoint: get(envCIS3Endpoint), Region: get(envCIS3Region), Bucket: get(envCIS3Bucket),
		Prefix: get(envCIS3Prefix), AccessKey: get(envCIS3AccessKey), SecretKey: get(envCIS3SecretKey)}
	if c.Bucket == "" {
		return errs
	}
	if c.Endpoint == "" || c.AccessKey == "" || c.SecretKey == "" {
		return append(errs, fmt.Sprintf("%s richiede anche %s, %s e %s", envCIS3Bucket, envCIS3Endpoint, envCIS3AccessKey, envCIS3SecretKey))
	}
	cfg.CIS3 = c
	return errs
}

// loadSMTP legge GITSTACK_CORE_SMTP_*. Senza HOST l'SMTP è spento e le altre
// variabili si ignorano (nessun errore: le notifiche restano in-app).
func loadSMTP(lookup func(string) (string, bool), cfg *Config, errs []string) []string {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	host := get(envSMTPHost)
	if host == "" {
		return errs
	}
	s := mailer.Config{Host: host, Security: mailer.SecurityStartTLS, Username: get(envSMTPUser), From: get(envSMTPFrom)}
	if v, ok := lookup(envSMTPPassword); ok {
		s.Password = v // la password non si taglia
	}
	switch sec := strings.ToLower(get(envSMTPSecurity)); sec {
	case "":
	case "tls", "starttls", "none":
		s.Security = mailer.Security(sec)
	default:
		errs = append(errs, fmt.Sprintf("%s non è valido: %q (tls|starttls|none)", envSMTPSecurity, sec))
	}
	s.Port = mailer.DefaultPort(s.Security)
	if v := get(envSMTPPort); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Sprintf("%s non è una porta valida (1-65535): %q", envSMTPPort, v))
		} else {
			s.Port = n
		}
	}
	if s.From == "" {
		errs = append(errs, fmt.Sprintf("%s è obbligatoria quando %s è impostata", envSMTPFrom, envSMTPHost))
	} else if _, err := mail.ParseAddress(s.From); err != nil {
		errs = append(errs, fmt.Sprintf("%s non è un indirizzo valido: %q", envSMTPFrom, s.From))
	}
	if (s.Username == "") != (s.Password == "") {
		errs = append(errs, fmt.Sprintf("%s e %s vanno impostate insieme (o nessuna dei due)", envSMTPUser, envSMTPPassword))
	}
	cfg.SMTP = s
	return errs
}

func parsePositiveInt32(v string) (int32, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
	if err != nil || n <= 0 || n > 1<<31-1 {
		return 0, fmt.Errorf("valore non valido: %q", v)
	}
	return int32(n), nil //nolint:gosec // G115: n e' limitato a MaxInt32 sopra
}
