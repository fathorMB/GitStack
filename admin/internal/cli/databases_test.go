package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

type secretsCluster struct {
	backup.Cluster
	secrets []backup.Secret
}

func (c *secretsCluster) Secrets(context.Context) ([]backup.Secret, error) { return c.secrets, nil }
func (c *secretsCluster) ApplySecret(_ context.Context, s backup.Secret) error {
	c.secrets = append(c.secrets, s)
	return nil
}

func writeRootCA(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA di prova"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, blk := range map[string]*pem.Block{"ca.crt": {Type: "CERTIFICATE", Bytes: der}, "ca.key": {Type: "PRIVATE KEY", Bytes: kd}} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(blk), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// `gitstack databases ensure` (lo lancia install.sh): crea una volta sola, poi lascia stare.
func TestDatabasesEnsure(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	writeRootCA(t, tlsDir)
	cl := &secretsCluster{}
	a, out, errb := hostApp(&recRunner{}, tlsDir)
	a.NewCluster = func(*config.Config) backup.Cluster { return cl }
	if code := a.Run(context.Background(), []string{"databases", "ensure", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(cl.secrets) != 2 || !strings.Contains(out.String(), "Chiave di dump creata") || !strings.Contains(out.String(), "CA intermedia dei database creata") {
		t.Fatalf("secrets=%d out=%s", len(cl.secrets), out)
	}
	first := string(cl.secrets[0].Raw) + string(cl.secrets[1].Raw)
	out.Reset()
	if code := a.Run(context.Background(), []string{"databases", "ensure", "--config", cfg}); code != ExitOK {
		t.Fatalf("secondo giro: exit %d: %s", code, errb)
	}
	if len(cl.secrets) != 2 || string(cl.secrets[0].Raw)+string(cl.secrets[1].Raw) != first || !strings.Contains(out.String(), "invariata") {
		t.Errorf("il secondo giro ha toccato i Secret: %d, %s", len(cl.secrets), out)
	}
}

func TestDatabasesEnsureSenzaRadice(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	cl := &secretsCluster{}
	a, out, errb := hostApp(&recRunner{}, tlsDir)
	a.NewCluster = func(*config.Config) backup.Cluster { return cl }
	if code := a.Run(context.Background(), []string{"databases", "ensure", "--config", cfg}); code != ExitConfig {
		t.Fatalf("exit %d, atteso ExitConfig: %s", code, errb)
	}
	if !strings.Contains(out.String(), "Chiave di dump creata") || !strings.Contains(errb.String(), "CA intermedia non creata") {
		t.Errorf("out=%s err=%s", out, errb)
	}
}

func TestDatabasesSottocomandoSconosciuto(t *testing.T) {
	a, _, _ := newApp(&fakeRunner{}, nil)
	if code := a.Run(context.Background(), []string{"databases", "boh"}); code != ExitUsage {
		t.Errorf("exit %d", code)
	}
}
