package status

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// N5 (GIT-143): con l'HTTPS dell'installer, /api/healthz si interroga in https
// fidandosi della CA interna (ca_cert) e senza disattivare la verifica.
func TestAPI_HTTPSConCAInterna(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(srv.URL, "https://")

	t.Run("con la CA: ok", func(t *testing.T) {
		ok, detail := (&Collector{}).api(context.Background(), &config.Config{Host: host, TLS: "internal", CACert: caPath})
		if !ok || !strings.HasPrefix(detail, "https://") {
			t.Fatalf("ok=%v detail=%q", ok, detail)
		}
	})
	t.Run("senza la CA: certificato non fidato", func(t *testing.T) {
		ok, detail := (&Collector{}).api(context.Background(), &config.Config{Host: host, TLS: "internal"})
		if ok {
			t.Fatalf("la verifica del certificato non deve essere saltata: %q", detail)
		}
	})
	t.Run("CA illeggibile: errore chiaro", func(t *testing.T) {
		ok, detail := (&Collector{}).api(context.Background(), &config.Config{Host: host, TLS: "internal", CACert: filepath.Join(t.TempDir(), "manca")})
		if ok || !strings.Contains(detail, "certificato della CA") {
			t.Fatalf("ok=%v detail=%q", ok, detail)
		}
	})
}

func TestAPI_InsecureResta_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, mode := range []string{"", "insecure"} {
		ok, detail := (&Collector{}).api(context.Background(), &config.Config{Host: host, TLS: mode})
		if !ok || !strings.HasPrefix(detail, "http://") {
			t.Fatalf("tls=%q ok=%v detail=%q", mode, ok, detail)
		}
	}
}
