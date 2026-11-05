package dbpki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
)

// fakeCluster implementa solo i due metodi che Ensure usa; gli altri metodi di backup.Cluster
// non esistono nel finto (l'interfaccia è incorporata e nil: chiamarli manda il test in panico).
type fakeCluster struct {
	backup.Cluster
	secrets []backup.Secret
	applied []string
}

func (f *fakeCluster) Secrets(context.Context) ([]backup.Secret, error) { return f.secrets, nil }
func (f *fakeCluster) ApplySecret(_ context.Context, s backup.Secret) error {
	f.applied = append(f.applied, s.Name)
	for i, e := range f.secrets {
		if e.Name == s.Name {
			f.secrets[i] = s
			return nil
		}
	}
	f.secrets = append(f.secrets, s)
	return nil
}

func (f *fakeCluster) get(t *testing.T, name string) map[string][]byte {
	t.Helper()
	for _, s := range f.secrets {
		if s.Name != name {
			continue
		}
		var obj struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(s.Raw, &obj); err != nil {
			t.Fatal(err)
		}
		if obj.Metadata.Labels["app.kubernetes.io/part-of"] != "gitstack" {
			t.Errorf("%s: manca la label part-of=gitstack (non entrerebbe in secrets.json)", name)
		}
		if obj.Metadata.Annotations["helm.sh/resource-policy"] != "keep" {
			t.Errorf("%s: manca helm.sh/resource-policy: keep", name)
		}
		out := map[string][]byte{}
		for k, v := range obj.Data {
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Fatal(err)
			}
			out[k] = b
		}
		return out
	}
	t.Fatalf("Secret %s non trovato", name)
	return nil
}

// newRoot scrive ca.crt e ca.key (PKCS#8, come `openssl genpkey`) in dir e ritorna la radice.
func newRoot(t *testing.T, dir string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "GitStack CA di prova"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), 0o600))
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// leaf firma una foglia per i SAN dati con l'intermedia (certPEM, keyPEM del Secret).
func leaf(t *testing.T, interCert, interKey []byte, dns []string, ips []string) *x509.Certificate {
	t.Helper()
	ic, err := parseCert(interCert)
	must(t, err)
	ik, err := parseKey(interKey)
	must(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "db"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
	}
	for _, s := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(s))
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ic, &key.PublicKey, ik)
	must(t, err)
	c, err := x509.ParseCertificate(der)
	must(t, err)
	return c
}

func verify(t *testing.T, root *x509.Certificate, interCert []byte, l *x509.Certificate, host string) error {
	t.Helper()
	ic, err := parseCert(interCert)
	must(t, err)
	roots, inter := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root)
	inter.AddCert(ic)
	_, err = l.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, DNSName: host,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

func writeTLSConf(t *testing.T, dir string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(dir, "tls.conf"), []byte("TLS_NAMES=git.acme.test,gitstack\nTLS_IPS=10.1.2.3\n"), 0o600))
}

func TestIntermediate_NameConstraints(t *testing.T) {
	dir := t.TempDir()
	root := newRoot(t, dir, time.Now().Add(10*365*24*time.Hour))
	writeTLSConf(t, dir)
	f := &fakeCluster{}
	res, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir})
	must(t, err)
	if !res.CreatedCA || !res.CreatedDumpKey {
		t.Fatalf("Ensure doveva creare intermedia e chiave di dump: %+v", res)
	}
	sec := f.get(t, "gitstack-db-ca")
	ic, err := parseCert(sec[CACertKey])
	must(t, err)
	if !ic.IsCA || !ic.MaxPathLenZero || ic.MaxPathLen != 0 {
		t.Errorf("l'intermedia deve essere una CA con pathlen 0: %+v", ic)
	}
	if !ic.PermittedDNSDomainsCritical {
		t.Error("le name constraints devono essere critiche")
	}
	if len(ic.PermittedDNSDomains) == 0 || ic.PermittedDNSDomains[0] != "svc.cluster.local" {
		t.Errorf("PermittedDNSDomains = %v", ic.PermittedDNSDomains)
	}
	if ic.NotAfter.After(root.NotAfter) {
		t.Error("l'intermedia non può scadere dopo la radice")
	}

	// Foglia per <svc>.<ns>.svc.cluster.local: valida.
	host := "pg.gitstack-db-1234.svc.cluster.local"
	l := leaf(t, sec[CACertKey], sec[CAKeyKey], []string{host}, nil)
	if err := verify(t, root, sec[CACertKey], l, host); err != nil {
		t.Errorf("foglia %s doveva essere valida: %v", host, err)
	}
	// Nome dell'istanza e suo IP: validi.
	for _, h := range []string{"git.acme.test", "db.git.acme.test"} {
		if err := verify(t, root, sec[CACertKey], leaf(t, sec[CACertKey], sec[CAKeyKey], []string{h}, nil), h); err != nil {
			t.Errorf("foglia %s doveva essere valida: %v", h, err)
		}
	}
	if err := verify(t, root, sec[CACertKey], leaf(t, sec[CACertKey], sec[CAKeyKey], []string{host}, []string{"10.1.2.3"}), host); err != nil {
		t.Errorf("foglia con l'IP dell'istanza doveva essere valida: %v", err)
	}
	// Fuori dai vincoli: rifiutate.
	for _, h := range []string{"example.com", "www.google.com", "svc.cluster.local.evil.com", "kube-system.cluster.local"} {
		err := verify(t, root, sec[CACertKey], leaf(t, sec[CACertKey], sec[CAKeyKey], []string{h}, nil), h)
		var inv x509.CertificateInvalidError
		if !errors.As(err, &inv) || inv.Reason != x509.CANotAuthorizedForThisName {
			t.Errorf("foglia %s doveva essere rifiutata per le name constraints, errore: %v", h, err)
		}
	}
	// Un IP fuori dai vincoli è rifiutato anche se il nome è buono.
	if err := verify(t, root, sec[CACertKey], leaf(t, sec[CACertKey], sec[CAKeyKey], []string{host}, []string{"192.0.2.9"}), host); err == nil {
		t.Error("foglia con un IP fuori dai vincoli doveva essere rifiutata")
	}
	// Senza l'intermedia la foglia non arriva alla radice.
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := l.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err == nil {
		t.Error("la foglia non deve validare senza l'intermedia")
	}
}

func TestEnsure_NonRigeneraQuelloCheC(t *testing.T) {
	dir := t.TempDir()
	newRoot(t, dir, time.Now().Add(10*365*24*time.Hour))
	f := &fakeCluster{}
	o := Options{Cluster: f, Release: "gitstack", TLSDir: dir}
	if _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	ca1, key1 := f.get(t, "gitstack-db-ca"), f.get(t, "gitstack-db-dump-key")
	if len(key1[DumpKeyData]) != DumpKeyLen {
		t.Errorf("la chiave di dump deve essere di %d byte, trovati %d", DumpKeyLen, len(key1[DumpKeyData]))
	}
	n := len(f.applied)

	// Upgrade: i Secret ci sono, niente da creare, niente di cambiato (anche con una radice diversa).
	other := t.TempDir()
	newRoot(t, other, time.Now().Add(time.Hour))
	res, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: other})
	must(t, err)
	if res.CreatedCA || res.CreatedDumpKey || len(f.applied) != n {
		t.Fatalf("Ensure ha toccato Secret esistenti: %+v applicati=%v", res, f.applied)
	}
	ca2, key2 := f.get(t, "gitstack-db-ca"), f.get(t, "gitstack-db-dump-key")
	if string(ca1[CACertKey]) != string(ca2[CACertKey]) || string(ca1[CAKeyKey]) != string(ca2[CAKeyKey]) {
		t.Error("l'intermedia è cambiata")
	}
	if string(key1[DumpKeyData]) != string(key2[DumpKeyData]) {
		t.Error("la chiave di dump è cambiata")
	}
}

func TestEnsure_InstallazioneEsistenteSenzaIntermedia(t *testing.T) {
	// Upgrade di un'installazione che ha già la chiave di dump ma non l'intermedia: crea solo quella.
	dir := t.TempDir()
	newRoot(t, dir, time.Now().Add(10*365*24*time.Hour))
	f := &fakeCluster{}
	if _, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir}); err != nil {
		t.Fatal(err)
	}
	f.secrets = f.secrets[:0:0]
	keep, err := secret("gitstack-db-dump-key", "gitstack", map[string][]byte{DumpKeyData: []byte(strings.Repeat("k", 32))})
	must(t, err)
	f.secrets = append(f.secrets, keep)
	f.applied = nil
	res, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir})
	must(t, err)
	if !res.CreatedCA || res.CreatedDumpKey || len(f.applied) != 1 || f.applied[0] != "gitstack-db-ca" {
		t.Fatalf("doveva creare solo l'intermedia: %+v applicati=%v", res, f.applied)
	}
}

func TestEnsure_SenzaRadice(t *testing.T) {
	f := &fakeCluster{}
	res, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: t.TempDir()})
	if !errors.Is(err, ErrNoRootCA) {
		t.Fatalf("errore = %v, atteso ErrNoRootCA", err)
	}
	if !res.CreatedDumpKey || res.CreatedCA {
		t.Errorf("la chiave di dump si crea anche senza radice: %+v", res)
	}
}

func TestNewIntermediate_RifiutaNomiStrani(t *testing.T) {
	dir := t.TempDir()
	newRoot(t, dir, time.Now().Add(time.Hour))
	crt, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	key, _ := os.ReadFile(filepath.Join(dir, "ca.key"))
	if _, _, err := NewIntermediate(crt, key, []string{"*.acme.test"}, nil, time.Now()); err == nil {
		t.Error("un nome con wildcard doveva essere rifiutato")
	}
	if _, _, err := NewIntermediate(crt, key, nil, []string{"non-un-ip"}, time.Now()); err == nil {
		t.Error("un IP non valido doveva essere rifiutato")
	}
}

func TestEnsure_ScriveLaRadicaPubblicaNelSecretDellaCA(t *testing.T) {
	dir := t.TempDir()
	newRoot(t, dir, time.Now().Add(10*365*24*time.Hour))
	f := &fakeCluster{}
	if _, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir}); err != nil {
		t.Fatal(err)
	}
	root, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	sec := f.get(t, "gitstack-db-ca")
	if string(sec[RootCertKey]) != string(root) {
		t.Fatalf("ca.crt del Secret dei database deve essere la radice pubblica dell'istanza")
	}
	if strings.Contains(string(sec[RootCertKey]), "PRIVATE KEY") {
		t.Fatal("la radice pubblica non puo' contenere una chiave")
	}
}

func TestEnsure_AggiungeLaRadiceAUnaCAEsistenteSenzaToccareLeChiavi(t *testing.T) {
	// Installazione di GIT-551: l'intermedia c'e', la radice pubblica no.
	dir := t.TempDir()
	newRoot(t, dir, time.Now().Add(10*365*24*time.Hour))
	f := &fakeCluster{}
	if _, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir}); err != nil {
		t.Fatal(err)
	}
	before := f.get(t, "gitstack-db-ca")
	old, err := secret("gitstack-db-ca", "gitstack", map[string][]byte{CACertKey: before[CACertKey], CAKeyKey: before[CAKeyKey]})
	must(t, err)
	for i := range f.secrets {
		if f.secrets[i].Name == "gitstack-db-ca" {
			f.secrets[i] = old
		}
	}
	f.applied = nil
	res, err := Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir})
	must(t, err)
	if res.CreatedCA || !res.AddedRoot || len(f.applied) != 1 {
		t.Fatalf("doveva solo aggiungere la radice: %+v applicati=%v", res, f.applied)
	}
	after := f.get(t, "gitstack-db-ca")
	if string(after[CACertKey]) != string(before[CACertKey]) || string(after[CAKeyKey]) != string(before[CAKeyKey]) {
		t.Fatal("intermedia e chiave devono restare quelle")
	}
	if len(after[RootCertKey]) == 0 {
		t.Fatal("ca.crt non scritto")
	}
	// Idempotente: il giro dopo non applica niente.
	f.applied = nil
	res, err = Ensure(context.Background(), Options{Cluster: f, Release: "gitstack", TLSDir: dir})
	must(t, err)
	if res.AddedRoot || len(f.applied) != 0 {
		t.Fatalf("secondo giro: %+v applicati=%v", res, f.applied)
	}
}
