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
	// EnvServiceSecret: segreto condiviso storico (F9), vale solo per i
	// segreti per chiamante che mancano.
	EnvServiceSecret = "GITSTACK_IDENTITY_SERVICE_SECRET"
	// EnvSecretGit: segreto di servizio del servizio git (chiave `git`),
	// con cui firma verso identity (Bearer) e core (identità).
	EnvSecretGit = "GITSTACK_SERVICE_SECRET_GIT"
	// EnvSecretCore: segreto di servizio di core, con cui verifica le
	// chiamate a /internal/*.
	EnvSecretCore = "GITSTACK_SERVICE_SECRET_CORE"
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
	// EnvMirrorMaxFetch: tetto di crescita del repo in un singolo fetch di un
	// pull mirror (F18, V12.1.1). Oltre il fetch è interrotto e l'aggiornamento
	// fallisce. Stesso formato; default 5GB; 0 = nessun tetto.
	EnvMirrorMaxFetch = "GITSTACK_GIT_MIRROR_MAX_FETCH_SIZE"
	// EnvReceiveFsck: `off` spegne receive.fsckObjects sul receive-pack (F18);
	// vuoto o `on` lo lascia attivo (default).
	EnvReceiveFsck = "GITSTACK_GIT_RECEIVE_FSCK"
	EnvCoreURL     = "GITSTACK_CORE_URL"
	// EnvNatsURL: indirizzo del bus NATS JetStream (es. nats://nats:4222) su
	// cui pubblicare git.push. Vuoto = nessun evento (il servizio lo dice nel
	// log all'avvio); NATS non raggiungibile non ferma né i push né l'avvio.
	EnvNatsURL = "GITSTACK_GIT_NATS_URL"
	// EnvMirrorCAFile: file PEM con le CA di cui il mirror in push si fida
	// (GIT-181), per una destinazione con certificato di una CA privata, per
	// esempio un secondo GitStack con la CA interna. SOSTITUISCE le CA di
	// sistema per i soli push dei mirror: per spingere anche su GitHub il file
	// deve contenerle tutte. Vuoto = CA di sistema.
	EnvMirrorCAFile = "GITSTACK_GIT_MIRROR_CA_FILE"

	// Registry OCI (GIT-286, RG1). È servito da questo servizio sotto /v2/.

	// EnvRegistry: off lo disattiva (/v2/ risponde 404); vuoto o on lo attiva
	// (default), ma parte solo con identity, core e segreto di servizio.
	EnvRegistry = "GITSTACK_GIT_REGISTRY"
	// EnvRegistryDir: cartella dei dati del registry su disco (default <dati>/registry).
	// Ignorata con un bucket S3.
	EnvRegistryDir = "GITSTACK_GIT_REGISTRY_DIR"
	// EnvCIGatewayURL: URL interno del ci-gateway di core, con cui il token del job
	// (gsj_) si trasforma nei permessi sul registry del suo repo. Vuoto = i token del
	// job non entrano (503); i token personali funzionano comunque.
	EnvCIGatewayURL = "GITSTACK_GIT_CI_GATEWAY_URL"
	// Archiviazione S3 del registry (al posto del disco): bucket obbligatorio, il resto
	// facoltativo. Chiavi e segreto arrivano da un Secret, mai dai valori del chart.
	EnvRegistryS3Bucket    = "GITSTACK_GIT_REGISTRY_S3_BUCKET"
	EnvRegistryS3Region    = "GITSTACK_GIT_REGISTRY_S3_REGION"
	EnvRegistryS3Endpoint  = "GITSTACK_GIT_REGISTRY_S3_ENDPOINT"
	EnvRegistryS3AccessKey = "GITSTACK_GIT_REGISTRY_S3_ACCESS_KEY"
	EnvRegistryS3SecretKey = "GITSTACK_GIT_REGISTRY_S3_SECRET_KEY"
	// EnvRegistryS3PathStyle: on per gli indirizzi http://endpoint/bucket/chiave (MinIO).
	EnvRegistryS3PathStyle = "GITSTACK_GIT_REGISTRY_S3_PATH_STYLE"
	// EnvRegistryS3Prefix: cartella dentro il bucket (default gitstack-registry).
	EnvRegistryS3Prefix = "GITSTACK_GIT_REGISTRY_S3_PREFIX"
)

// DefaultSSHAddr è la porta SSH di default (R7).
const DefaultSSHAddr = ":2222"

// Config è la configurazione del servizio git.
type Config struct {
	Addr     string
	DataDir  string
	LogLevel string
	// ServiceSecret è il segreto proprio del servizio git: firma le
	// chiamate verso identity e core. Non va mai nei log né negli errori.
	// Un elenco "nuova,vecchia" è accettato (rotazione): firma con la prima.
	ServiceSecret string
	// CoreSecret è il segreto di core: verifica le chiamate a /internal/*;
	// vuoto = ogni chiamata interna è rifiutata con 401.
	CoreSecret string
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
	// MirrorMaxFetchBytes: vedi EnvMirrorMaxFetch (0 = nessun tetto).
	MirrorMaxFetchBytes int64
	// NoFsck: vedi EnvReceiveFsck.
	NoFsck bool
	// NatsURL: vuoto = git.push non viene pubblicato.
	NatsURL string
	// MirrorCAFile: vedi EnvMirrorCAFile; vuoto = CA di sistema.
	MirrorCAFile string
	// RegistryOff: il registry OCI è disattivato (EnvRegistry=off).
	RegistryOff bool
	// RegistryDir: cartella dei dati del registry su disco (valorizzata se c'è DataDir).
	RegistryDir string
	// CIGatewayURL: vedi EnvCIGatewayURL.
	CIGatewayURL string
	// RegistryS3 non nil = il registry archivia su S3.
	RegistryS3 *RegistryS3
}

// RegistryS3 è l'archiviazione S3 del registry.
type RegistryS3 struct {
	Bucket, Region, Endpoint, AccessKey, SecretKey, Prefix string
	PathStyle                                              bool
}

// Load legge la configurazione dall'ambiente di processo.
func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	var errs []string
	cfg := Config{Addr: ":8080", LogLevel: "info", SSHAddr: DefaultSSHAddr, MaxBlobBytes: 100 << 20, RepoWarnBytes: 5 << 30, MirrorMaxFetchBytes: 5 << 30}

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
	legacy := ""
	if v, ok := lookup(EnvServiceSecret); ok {
		legacy = strings.TrimSpace(v)
	}
	pick := func(env string) string {
		if v, ok := lookup(env); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return legacy
	}
	cfg.ServiceSecret = pick(EnvSecretGit)
	cfg.CoreSecret = pick(EnvSecretCore)

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
	if v, ok := lookup(EnvNatsURL); ok {
		cfg.NatsURL = strings.TrimSpace(v)
	}
	if v, ok := lookup(EnvReceiveFsck); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "on":
		case "off":
			cfg.NoFsck = true
		default:
			errs = append(errs, fmt.Sprintf("%s deve essere on oppure off: %q", EnvReceiveFsck, v))
		}
	}
	if v, ok := lookup(EnvMirrorCAFile); ok {
		cfg.MirrorCAFile = strings.TrimSpace(v)
	}

	if v, ok := lookup(EnvRegistry); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "on":
		case "off":
			cfg.RegistryOff = true
		default:
			errs = append(errs, fmt.Sprintf("%s deve essere on oppure off: %q", EnvRegistry, v))
		}
	}
	if v, ok := lookup(EnvRegistryDir); ok && strings.TrimSpace(v) != "" {
		cfg.RegistryDir = strings.TrimSpace(v)
	} else if cfg.DataDir != "" {
		cfg.RegistryDir = filepath.Join(cfg.DataDir, "registry")
	}
	if v, ok := lookup(EnvCIGatewayURL); ok {
		cfg.CIGatewayURL = strings.TrimSpace(v)
	}
	s3get := func(env string) string {
		v, _ := lookup(env)
		return strings.TrimSpace(v)
	}
	s3 := RegistryS3{Bucket: s3get(EnvRegistryS3Bucket), Region: s3get(EnvRegistryS3Region), Endpoint: s3get(EnvRegistryS3Endpoint),
		AccessKey: s3get(EnvRegistryS3AccessKey), SecretKey: s3get(EnvRegistryS3SecretKey), Prefix: s3get(EnvRegistryS3Prefix)}
	switch strings.ToLower(s3get(EnvRegistryS3PathStyle)) {
	case "", "off":
	case "on":
		s3.PathStyle = true
	default:
		errs = append(errs, fmt.Sprintf("%s deve essere on oppure off: %q", EnvRegistryS3PathStyle, s3get(EnvRegistryS3PathStyle)))
	}
	switch {
	case s3.Bucket != "":
		if (s3.AccessKey == "") != (s3.SecretKey == "") {
			errs = append(errs, fmt.Sprintf("%s e %s vanno impostate insieme", EnvRegistryS3AccessKey, EnvRegistryS3SecretKey))
		}
		cfg.RegistryS3 = &s3
	case s3.Region != "" || s3.Endpoint != "" || s3.AccessKey != "" || s3.SecretKey != "" || s3.Prefix != "":
		errs = append(errs, fmt.Sprintf("%s è obbligatoria se si configura S3 per il registry", EnvRegistryS3Bucket))
	}

	for _, s := range []struct {
		env string
		dst *int64
	}{{EnvMaxBlobSize, &cfg.MaxBlobBytes}, {EnvRepoSizeWarn, &cfg.RepoWarnBytes}, {EnvMirrorMaxFetch, &cfg.MirrorMaxFetchBytes}} {
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
