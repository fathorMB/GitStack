package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// acmeCluster è fakeCluster più il volume ACME di Traefik.
type acmeCluster struct {
	*fakeCluster
	dir      string // cartella del volume; "" = Let's Encrypt non acceso
	replicas int
	order    []string
}

func (c *acmeCluster) ACMEPath(context.Context) (string, error) {
	if c.dir == "" {
		return "", ErrNotFound
	}
	return c.dir, nil
}

func (c *acmeCluster) StopTraefik(context.Context) (int, error) {
	c.order = append(c.order, "stop traefik")
	return c.replicas, nil
}

func (c *acmeCluster) StartTraefik(_ context.Context, n int) error {
	c.order = append(c.order, "start traefik")
	if n != c.replicas {
		return errors.New("repliche di Traefik diverse da quelle di prima")
	}
	return nil
}

const (
	acmeJSON = `{"letsencrypt":{"Account":{"Email":"a@b.it"},"Certificates":[{"domain":{"main":"app.example.org"}}]}}`
	// Il database ha due domini (uno Let's Encrypt, uno con certificato del cliente) e la chiave del
	// cliente solo cifrata (cert_key_ciphertext): il PEM della chiave non compare mai in chiaro.
	acmeDump = "INSERT INTO core.app_domains (id, normalized, cert_pem, cert_key_ciphertext) VALUES " +
		"('d1', 'le.example.org', NULL, NULL), ('d2', 'custom.example.org', 'CERT-PEM', '\\x6369667261746f');\n"
)

func newACMEEnv(t *testing.T) (*env, *acmeCluster) {
	t.Helper()
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "traefik-pvc")
	writeFile(t, filepath.Join(dir, "acme.json"), acmeJSON)
	if err := os.Chmod(filepath.Join(dir, "acme.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.f.dump = acmeDump
	c := &acmeCluster{fakeCluster: e.f, dir: dir, replicas: 1}
	e.o.Cluster = c
	return e, c
}

// Backup → restore con un dominio Let's Encrypt e uno con certificato del cliente: i domini e il
// certificato (con la chiave solo cifrata) viaggiano nel database, l'archivio ACME torna nel
// volume di Traefik con Traefik fermo, e il file mantiene i permessi 0600.
func TestBackupRestoreDominiCertificatiEACME(t *testing.T) {
	e, _ := newACMEEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if names := tarNames(t, res.Path, ACMEName); strings.Join(names, ",") != "acme.json" {
		t.Fatalf("contenuto dell'archivio ACME: %v", names)
	}
	found := false
	for _, f := range res.Manifest.Files {
		found = found || f.Name == ACMEName
	}
	if !found {
		t.Fatal("traefik-acme.tar non è nel manifest")
	}

	// Restore su una VM pulita: il volume di Traefik ha un acme.json vuoto di un'altra installazione.
	r, rc := newACMEEnv(t)
	writeFile(t, filepath.Join(rc.dir, "acme.json"), "{}")
	writeFile(t, filepath.Join(rc.dir, "vecchio"), "x")
	r.f.dump = ""
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(rc.dir, "acme.json"))
	if err != nil || string(b) != acmeJSON {
		t.Errorf("acme.json = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(rc.dir, "vecchio")); err == nil {
		t.Error("il contenuto precedente del volume ACME non è stato tolto")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(filepath.Join(rc.dir, "acme.json")); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("permessi di acme.json: %v, %v", st, err)
		}
	}
	if got := strings.Join(rc.order, "|"); got != "stop traefik|start traefik" {
		t.Errorf("ordine: %s", got)
	}
	// I domini e il certificato sono nel database ripristinato; la chiave resta cifrata.
	in := r.f.psqlIn
	for _, want := range []string{"le.example.org", "custom.example.org", "CERT-PEM", "cert_key_ciphertext"} {
		if !strings.Contains(in, want) {
			t.Errorf("il database ripristinato non contiene %q", want)
		}
	}
	if strings.Contains(in, "PRIVATE KEY") {
		t.Error("chiave privata in chiaro nel database")
	}
	if strings.Contains(r.log.String(), "acme.json") || strings.Contains(r.log.String(), "a@b.it") {
		t.Errorf("dati ACME nei log: %s", r.log.String())
	}
}

// Senza Let's Encrypt (nessun volume di Traefik) il backup non ha la voce e il restore non cerca niente.
func TestBackupSenzaACME(t *testing.T) {
	e, c := newACMEEnv(t)
	c.dir = ""
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Manifest.Files {
		if f.Name == ACMEName {
			t.Fatal("archivio ACME inatteso")
		}
	}
	if len(c.order) != 0 {
		t.Errorf("Traefik toccato: %v", c.order)
	}
}

// Un archivio con i certificati ACME su un'installazione senza il volume di Traefik: avviso, non errore.
func TestRestoreACMESenzaVolumeAvvisa(t *testing.T) {
	e, _ := newACMEEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r, rc := newACMEEnv(t)
	rc.dir = ""
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.log.String(), "non ripristinati") || len(rc.order) != 0 {
		t.Errorf("log: %s, ordine: %v", r.log.String(), rc.order)
	}
}
