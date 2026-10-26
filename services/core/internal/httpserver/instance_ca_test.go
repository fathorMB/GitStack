package httpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

func writeTestCA(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "GitStack test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func getCA(t *testing.T, c CAConfig) openapi.InstanceCa {
	t.Helper()
	s := &apiServer{instanceCA: c}
	rec := httptest.NewRecorder()
	s.GetInstanceCa(rec, httptest.NewRequest(http.MethodGet, "/instance/ca", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out openapi.InstanceCa
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// L'impronta coincide con quella di `openssl x509 -fingerprint -sha256` sullo stesso file.
func TestInstanceCa_ImprontaCoincideConOpenssl(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl non trovato nel PATH")
	}
	dir := t.TempDir()
	writeTestCA(t, dir)
	out, err := exec.Command(openssl, "x509", "-in", filepath.Join(dir, "ca.crt"), "-noout", "-fingerprint", "-sha256").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "sha256 Fingerprint="))
	want = strings.TrimPrefix(want, "SHA256 Fingerprint=")
	got := getCA(t, CAConfig{Dir: dir})
	if got.Mode != openapi.InstanceCaModeInternal || got.FingerprintSha256 == nil || *got.FingerprintSha256 != want {
		t.Fatalf("risposta %+v, impronta voluta %q", got, want)
	}
	if got.DownloadUrl == nil || *got.DownloadUrl != "/downloads/ca.crt" {
		t.Fatalf("downloadUrl = %v", got.DownloadUrl)
	}
}

// Senza CA interna la risposta lo dice: custom se dichiarato dal chart, altrimenti none; mai un'impronta.
func TestInstanceCa_SenzaCaInterna(t *testing.T) {
	empty := t.TempDir()
	for name, c := range map[string]CAConfig{
		"nessuna cartella": {},
		"cartella vuota":   {Dir: empty},
		"cartella assente": {Dir: filepath.Join(empty, "assente"), Mode: "custom"},
		"modo custom":      {Dir: empty, Mode: "custom"},
	} {
		got := getCA(t, c)
		want := openapi.InstanceCaModeNone
		if c.Mode == "custom" {
			want = openapi.InstanceCaModeCustom
		}
		if got.Mode != want || got.FingerprintSha256 != nil || got.DownloadUrl != nil {
			t.Errorf("%s: %+v, modo voluto %s", name, got, want)
		}
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "ca.crt"), []byte("non un certificato"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := getCA(t, CAConfig{Dir: bad}); got.Mode != openapi.InstanceCaModeNone || got.FingerprintSha256 != nil {
		t.Errorf("ca.crt illeggibile: %+v", got)
	}
}
