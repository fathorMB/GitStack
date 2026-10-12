// Package config legge e valida /etc/gitstack/config.yaml, il file di
// configurazione dell'installazione scritto da deploy/install.sh e letto
// dai comandi di `gitstack`.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath è la posizione del file scritto dall'installer.
const DefaultPath = "/etc/gitstack/config.yaml"

// SupportedVersion è la versione dello schema del file.
const SupportedVersion = 1

// Config è il contenuto di config.yaml.
type Config struct {
	// Version è la versione dello schema (oggi 1).
	Version int `yaml:"version"`
	// Host è il nome o l'IP con cui si raggiunge GitStack (UI e API).
	Host string `yaml:"host"`
	// SSHPort è la porta SSH del servizio git (mai la 22 dell'host).
	SSHPort int `yaml:"ssh_port"`
	// TLS è la modalità HTTPS scelta dall'installer: internal, custom,
	// letsencrypt o insecure (solo HTTP). Vuoto = installazione precedente a
	// N5 (GIT-143): HTTP.
	TLS string `yaml:"tls"`
	// CACert è il certificato della CA interna (solo con tls: internal): i
	// comandi che parlano con GitStack in HTTPS si fidano di questo.
	CACert string `yaml:"ca_cert"`
	// ChartDir è la copia locale del chart Helm, scritta da install.sh: serve
	// a `gitstack config set host` per aggiornare i servizi.
	ChartDir string `yaml:"chart_dir"`
	// Release è il nome della release Helm.
	Release string `yaml:"release"`
	// Namespace è il namespace Kubernetes della release.
	Namespace string `yaml:"namespace"`
	// ImageTag è il tag delle immagini installate (versione del server).
	ImageTag string `yaml:"image_tag"`
	// Kubeconfig è il kubeconfig con cui parlare al cluster.
	Kubeconfig string `yaml:"kubeconfig"`
	// Backup descrive dove e per quanto si conservano i backup.
	Backup Backup `yaml:"backup"`
	// Postgres descrive il database esterno (N8). Vuoto = Postgres bundle.
	Postgres Postgres `yaml:"postgres"`
	// V5 è la sezione della catena di fornitura (firme cosign), scritta
	// dall'installer e letta da `gitstack upgrade` e `gitstack status`.
	V5 V5 `yaml:"v5"`
}

// V5 è la sezione v5 del file di configurazione.
type V5 struct {
	// AllowUnsigned è l'eccezione N9: l'installazione usa un mirror interno
	// non firmato e la verifica cosign di binario, chart e immagini è
	// disattivata. Mai il default: la scrive l'installer solo con
	// --allow-unsigned-mirrors, e `gitstack status` la segnala.
	AllowUnsigned bool `yaml:"allow_unsigned"`
	// CosignPath è il cosign pinnato installato da deploy/install.sh
	// (vuoto: upgrade.DefaultCosignPath).
	CosignPath string `yaml:"cosign_path"`
}

// Postgres è la sezione postgres del file: scritta dall'installer quando si
// usa --postgres-url. Mai password, mai l'URL completo: solo ciò che serve a
// capire dove sta il database (la password sta nel Secret del cluster).
type Postgres struct {
	// External è vero con un Postgres del cliente (chart postgres.enabled=false).
	External bool   `yaml:"external"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	// SSLMode è il sslmode del database (require, verify-full...).
	SSLMode string `yaml:"ssl_mode"`
	// Version è la versione del server rilevata dal preflight dell'installer
	// (es. 16.4); `gitstack status` mostra quella letta ora, se può.
	Version string `yaml:"version"`
	// PasswordSecret e CASecret sono i Secret del cluster con la password e
	// la CA del database (creati dall'installer, mai ripristinati da un
	// backup: appartengono al Postgres di questa installazione).
	PasswordSecret string `yaml:"password_secret"`
	CASecret       string `yaml:"ca_secret"`
}

// Backup è la sezione backup del file.
type Backup struct {
	// Destination è la cartella dei backup.
	Destination string `yaml:"destination"`
	// Retention è il numero di backup conservati.
	Retention int `yaml:"retention"`
	// S3 è la copia su storage compatibile S3 (N7); nil = solo locale.
	S3 *S3 `yaml:"s3,omitempty"`
}

// ErrNotFound indica che il file non esiste (GitStack non installato o
// installato senza install.sh recente).
var ErrNotFound = errors.New("file di configurazione non trovato")

// ErrPermission indica che il file non è leggibile da questo utente.
var ErrPermission = errors.New("file di configurazione non leggibile")

// ErrInvalid indica un file illeggibile come YAML o con valori non validi.
var ErrInvalid = errors.New("file di configurazione non valido")

// Load legge e valida il file in path. Rifiuta un file leggibile da gruppo o
// altri (deve essere root-only, 0600): contiene percorsi e nomi interni
// dell'installazione e i comandi futuri vi aggiungeranno segreti.
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, wrap(path, err)
	}
	if err := checkMode(info.Mode()); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrap(path, err)
	}
	return Parse(data, path)
}

// Parse decodifica e valida il contenuto (name serve solo ai messaggi).
func Parse(data []byte, name string) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = SupportedVersion
	}
	if c.Release == "" {
		c.Release = "gitstack"
	}
	if c.Namespace == "" {
		c.Namespace = "default"
	}
	if c.SSHPort == 0 {
		c.SSHPort = 2222
	}
	if c.Kubeconfig == "" {
		c.Kubeconfig = "/etc/rancher/k3s/k3s.yaml"
	}
	if c.Postgres.External && c.Postgres.Port == 0 {
		c.Postgres.Port = 5432
	}
	if c.Backup.Destination == "" {
		c.Backup.Destination = "/var/backups/gitstack"
	}
	if c.Backup.Retention == 0 {
		c.Backup.Retention = 7
	}
}

// Validate controlla i valori.
func (c *Config) Validate() error {
	if c.Version != SupportedVersion {
		return fmt.Errorf("version %d non supportata (attesa %d)", c.Version, SupportedVersion)
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host mancante")
	}
	if strings.ContainsAny(c.Host, " /\t") {
		return fmt.Errorf("host %q non valido (nome o IP, senza schema)", c.Host)
	}
	if c.SSHPort < 1 || c.SSHPort > 65535 {
		return fmt.Errorf("ssh_port %d fuori intervallo", c.SSHPort)
	}
	if c.Postgres.External {
		if strings.TrimSpace(c.Postgres.Host) == "" || strings.TrimSpace(c.Postgres.Database) == "" {
			return errors.New("postgres.external richiede postgres.host e postgres.database")
		}
		if c.Postgres.Port < 0 || c.Postgres.Port > 65535 {
			return fmt.Errorf("postgres.port %d fuori intervallo", c.Postgres.Port)
		}
	}
	if c.Backup.Retention < 1 {
		return fmt.Errorf("backup.retention %d non valido (almeno 1)", c.Backup.Retention)
	}
	if !strings.HasPrefix(c.Backup.Destination, "/") {
		return fmt.Errorf("backup.destination %q deve essere un percorso assoluto", c.Backup.Destination)
	}
	if c.Backup.S3 != nil {
		if err := c.Backup.S3.Validate(); err != nil {
			return fmt.Errorf("backup.s3: %w", err)
		}
	}
	return nil
}

func wrap(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %s", ErrPermission, path)
	default:
		return fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
	}
}

// SetFields cambia (o aggiunge in fondo) le chiavi di primo livello indicate,
// conservando commenti e resto, e riscrive il file in modo atomico con modo
// 0600. Nessuna chiave annidata: servono solo scalari.
func SetFields(path string, fields map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	done := map[string]bool{}
	for i, l := range lines {
		for k, v := range fields {
			if strings.HasPrefix(l, k+":") {
				lines[i] = k + ": " + v
				done[k] = true
			}
		}
	}
	// Le chiavi nuove vanno prima della sezione backup (o in fondo): una
	// riga di primo livello dopo `backup:` resterebbe comunque valida, ma
	// così il file resta leggibile.
	var extra []string
	for _, k := range sortedKeys(fields) {
		if !done[k] {
			extra = append(extra, k+": "+fields[k])
		}
	}
	if len(extra) > 0 {
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, extra...)
		lines = append(lines, "")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
