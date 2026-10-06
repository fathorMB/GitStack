// Package config legge e valida /etc/gitstack/config.yaml, il file di
// configurazione dell'installazione scritto da deploy/install.sh e letto
// dai comandi di `gitstack`.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
}

// Backup è la sezione backup del file.
type Backup struct {
	// Destination è la cartella dei backup.
	Destination string `yaml:"destination"`
	// Retention è il numero di backup conservati.
	Retention int `yaml:"retention"`
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
	if c.Backup.Retention < 1 {
		return fmt.Errorf("backup.retention %d non valido (almeno 1)", c.Backup.Retention)
	}
	if !strings.HasPrefix(c.Backup.Destination, "/") {
		return fmt.Errorf("backup.destination %q deve essere un percorso assoluto", c.Backup.Destination)
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
