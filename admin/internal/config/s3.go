package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultS3Retention è il numero di archivi conservati su S3 se non si
// sceglie altro (N7).
const DefaultS3Retention = 30

// S3RetentionBucket lascia la conservazione al bucket (lifecycle del
// provider): gitstack non cancella mai niente su S3.
const S3RetentionBucket = "bucket"

// DefaultS3Region è la regione se non indicata (MinIO e simili la ignorano).
const DefaultS3Region = "us-east-1"

// S3 è la sezione backup.s3: copia cifrata dei backup su un servizio
// compatibile S3 (AWS, MinIO, Backblaze B2, Wasabi). Contiene le chiavi di
// accesso: il file di configurazione è root-only (0600) e Load lo impone.
type S3 struct {
	// Endpoint è host[:porta] o un URL con schema (http:// per MinIO in
	// chiaro). Senza schema si usa HTTPS.
	Endpoint string `yaml:"endpoint"`
	Bucket   string `yaml:"bucket"`
	// Prefix è la «cartella» nel bucket (senza barra iniziale).
	Prefix string `yaml:"prefix,omitempty"`
	Region string `yaml:"region,omitempty"`
	// AccessKey e SecretKey sono le chiavi del servizio. Mai nei log.
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	// KeyFile è il file della chiave di cifratura (la stessa di --key-file):
	// obbligatorio, gli archivi caricati sono sempre cifrati.
	KeyFile string `yaml:"key_file"`
	// Retention: numero di archivi da tenere su S3 (default 30) oppure
	// «bucket» per lasciare la scelta al bucket.
	Retention string `yaml:"retention,omitempty"`
	// NotifyCommand, se non vuoto, gira con `sh -c` quando il caricamento
	// fallisce (variabili GITSTACK_BACKUP_*): vi si aggancia mail, ntfy, ecc.
	NotifyCommand string `yaml:"notify_command,omitempty"`
}

// String non mostra mai le chiavi: l'oggetto può finire per sbaglio in un log.
func (s S3) String() string {
	return fmt.Sprintf("s3{endpoint=%s bucket=%s prefix=%s region=%s access_key=*** secret_key=***}",
		s.Endpoint, s.Bucket, s.Prefix, s.Region)
}

// GoString idem per %#v.
func (s S3) GoString() string { return s.String() }

// KeepCount ritorna quanti archivi tenere su S3; 0 con «bucket» (nessuna
// potatura).
func (s S3) KeepCount() int {
	if strings.EqualFold(strings.TrimSpace(s.Retention), S3RetentionBucket) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(s.Retention))
	if err != nil || n < 1 {
		return DefaultS3Retention
	}
	return n
}

// RegionOrDefault è la regione da usare.
func (s S3) RegionOrDefault() string {
	if s.Region == "" {
		return DefaultS3Region
	}
	return s.Region
}

// Validate controlla la sezione. Senza chiave di cifratura la configurazione
// è rifiutata (N7): un archivio in chiaro non lascia mai la macchina.
func (s *S3) Validate() error {
	s.Prefix = strings.Trim(s.Prefix, "/")
	switch {
	case strings.TrimSpace(s.Endpoint) == "":
		return errors.New("endpoint mancante")
	case strings.TrimSpace(s.Bucket) == "":
		return errors.New("bucket mancante")
	case s.AccessKey == "" || s.SecretKey == "":
		return errors.New("access_key e secret_key sono obbligatorie")
	case strings.TrimSpace(s.KeyFile) == "":
		return errors.New("key_file mancante: gli archivi caricati su S3 sono sempre cifrati, serve il file della chiave (la stessa di --key-file)")
	case !filepath.IsAbs(s.KeyFile) && !strings.HasPrefix(s.KeyFile, "/"):
		return fmt.Errorf("key_file %q deve essere un percorso assoluto", s.KeyFile)
	}
	if strings.ContainsAny(s.Endpoint, " \t/") && !strings.Contains(s.Endpoint, "://") {
		return fmt.Errorf("endpoint %q non valido (host[:porta] oppure URL con schema)", s.Endpoint)
	}
	if r := strings.TrimSpace(s.Retention); r != "" && !strings.EqualFold(r, S3RetentionBucket) {
		if n, err := strconv.Atoi(r); err != nil || n < 1 {
			return fmt.Errorf("retention %q non valida (numero intero >= 1 oppure %q)", r, S3RetentionBucket)
		}
	}
	return nil
}

// SetBackupS3 scrive (o, con nil, toglie) la sezione backup.s3 del file,
// conservando commenti e resto, in modo atomico con modo 0600.
func SetBackupS3(path string, s *S3) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s: radice non valida", ErrInvalid, path)
	}
	root := doc.Content[0]
	backup := mapValue(root, "backup")
	if backup == nil {
		backup = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "backup"}, backup)
	}
	if backup.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s: backup non è una sezione", ErrInvalid, path)
	}
	// Tolgo la vecchia sezione.
	for i := 0; i+1 < len(backup.Content); i += 2 {
		if backup.Content[i].Value == "s3" {
			backup.Content = append(backup.Content[:i], backup.Content[i+2:]...)
			break
		}
	}
	if s != nil {
		var n yaml.Node
		if err := n.Encode(s); err != nil {
			return err
		}
		backup.Content = append(backup.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "s3"}, &n)
	}
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	// Il risultato deve restare valido per Parse.
	if _, err := Parse([]byte(out.String()), path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(out.String()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
