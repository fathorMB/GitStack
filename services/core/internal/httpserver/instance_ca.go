package httpserver

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// CAConfig dice a core dove trova la CA interna dell'istanza (H2, N5, GIT-383).
// Dir è il ConfigMap `ingress.tls.caConfigMap` montato come cartella intera
// (optional: un rinnovo arriva senza riavvio), con la chiave `ca.crt`. Mode è la
// modalità dichiarata dal chart quando la CA interna non c'è: `custom`
// (certificato del cliente o Let's Encrypt) o `none` (niente HTTPS).
type CAConfig struct {
	Dir  string
	Mode string
}

// WithInstanceCA imposta la CA interna letta da GET /instance/ca.
func WithInstanceCA(c CAConfig) Option {
	return func(o *routerOptions) { o.instanceCA = c }
}

// CAFingerprint legge ca.crt (PEM o DER) in dir e ne calcola lo SHA-256 del DER,
// nel formato di `openssl x509 -fingerprint -sha256`: byte maiuscoli separati da
// due punti. È l'unica fonte dell'impronta dell'istanza.
func CAFingerprint(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return "", err
	}
	der := raw
	if block, _ := pem.Decode(raw); block != nil {
		der = block.Bytes
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		return "", fmt.Errorf("ca.crt non è un certificato: %w", err)
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
}

// GetInstanceCa implementa GET /instance/ca, pubblica. Con la CA interna
// leggibile: mode internal, impronta e indirizzo del download. Altrimenti il
// modo dichiarato dal chart (custom o none): non c'è niente da installare.
func (s *apiServer) GetInstanceCa(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.instanceCa())
}

// instanceCa è la fonte unica dell impronta: la usano GET /instance/ca e l invito ai tester (GIT-376).
func (s *apiServer) instanceCa() openapi.InstanceCa {
	out := openapi.InstanceCa{Mode: openapi.InstanceCaModeNone}
	if s.instanceCA.Dir != "" {
		if fp, err := CAFingerprint(s.instanceCA.Dir); err == nil {
			url := "/downloads/ca.crt"
			out = openapi.InstanceCa{Mode: openapi.InstanceCaModeInternal, FingerprintSha256: &fp, DownloadUrl: &url}
		}
	}
	if out.Mode == openapi.InstanceCaModeNone && s.instanceCA.Mode == string(openapi.InstanceCaModeCustom) {
		out.Mode = openapi.InstanceCaModeCustom
	}
	return out
}
