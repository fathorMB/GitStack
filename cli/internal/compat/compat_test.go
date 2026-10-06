package compat

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func server(t *testing.T, version string, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/v1/meta" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_version":"` + version + `","api_version":"0.1.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func run(t *testing.T, srv *httptest.Server, client, cache string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := Check(context.Background(), srv.Client(), srv.URL+"/v1", client, cache, &buf)
	return buf.String(), err
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name, server, client string
		wantWarn, wantErr    bool
	}{
		{"uguale", "v1.2.3", "v1.2.3", false, false},
		{"minore diversa", "v1.3.0", "v1.2.3", true, false},
		{"patch diversa, con e senza v", "1.2.4", "v1.2.3", true, false},
		{"maggiore diversa", "v2.0.0", "v1.2.3", false, true},
		{"non semver uguale", "sha-abc123", "sha-abc123", false, false},
		{"non semver diversa", "sha-abc123", "v1.2.3", true, false},
		{"dev contro semver", "v3.0.0", "dev", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := server(t, tc.server, http.StatusOK)
			out, err := run(t, srv, tc.client, "")
			if tc.wantErr {
				if !errors.Is(err, ErrMajorMismatch) || !strings.Contains(err.Error(), tc.server) || !strings.Contains(err.Error(), tc.client) {
					t.Fatalf("errore = %v, voluto ErrMajorMismatch con le due versioni", err)
				}
			} else if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			if tc.wantWarn != (out != "") {
				t.Errorf("avviso = %q, wantWarn %v", out, tc.wantWarn)
			}
		})
	}
}

func TestCheck_404NessunAvviso(t *testing.T) {
	srv, _ := server(t, "", http.StatusNotFound)
	if out, err := run(t, srv, "v1.0.0", ""); out != "" || err != nil {
		t.Errorf("out %q err %v", out, err)
	}
}

func TestCheck_CacheValidaNessunaSecondaRichiesta(t *testing.T) {
	srv, n := server(t, "v1.3.0", http.StatusOK)
	cache := filepath.Join(t.TempDir(), "sub", "compat.json")
	out1, _ := run(t, srv, "v1.2.3", cache)
	out2, _ := run(t, srv, "v1.2.3", cache)
	if n.Load() != 1 {
		t.Errorf("richieste = %d, volute 1", n.Load())
	}
	if out1 == "" || out1 != out2 {
		t.Errorf("l'avviso deve ripetersi dalla cache: %q / %q", out1, out2)
	}
	// il rifiuto vale anche dalla cache
	if _, err := run(t, srv, "v2.0.0", cache); !errors.Is(err, ErrMajorMismatch) {
		t.Errorf("errore = %v", err)
	}
	// scaduta: nuova richiesta
	orig := now
	t.Cleanup(func() { now = orig })
	now = func() time.Time { return orig().Add(2 * CacheTTL) }
	_, _ = run(t, srv, "v1.2.3", cache)
	if n.Load() != 2 {
		t.Errorf("richieste = %d, volute 2 dopo la scadenza", n.Load())
	}
}

func TestCheck_ServerIrraggiungibileNonBlocca(t *testing.T) {
	srv, _ := server(t, "v1.0.0", http.StatusOK)
	srv.Close()
	if out, err := run(t, srv, "v9.0.0", ""); out != "" || err != nil {
		t.Errorf("out %q err %v", out, err)
	}
}
