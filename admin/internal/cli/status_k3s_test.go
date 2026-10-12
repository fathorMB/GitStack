package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// k3sStatusRunner risponde a `k3s --version` e al resto come kubectl.
type k3sStatusRunner struct{ version string }

func (r k3sStatusRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	if strings.HasSuffix(name, "k3s") && len(args) == 1 && args[0] == "--version" {
		return []byte("k3s version " + r.version + " (deadbeef)\ngo version go1.26\n"), nil
	}
	return []byte(kubectlJSON(1)), nil
}

func rawServer(t *testing.T, declared string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/deploy/k3s-version") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, declared+"\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStatusK3s(t *testing.T) {
	cases := []struct {
		name, installed, declared, want, update string
	}{
		{"aggiornata", "v1.36.4+k3s1", "v1.36.4+k3s1", "Versione k3s: v1.36.4+k3s1 (aggiornata)", ""},
		{"patch", "v1.36.4+k3s1", "v1.36.5+k3s1", "aggiornamento disponibile: v1.36.5+k3s1 (patch", "patch"},
		{"minore", "v1.36.4+k3s1", "v1.37.0+k3s1", "aggiornamento disponibile: v1.37.0+k3s1 (nuova minore: gitstack upgrade --k3s)", "minor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := healthServer(200)
			defer srv.Close()
			raw := rawServer(t, c.declared)
			a, out, errb := newApp(&fakeRunner{}, srv)
			a.Runner = k3sStatusRunner{version: c.installed}
			a.Getenv = func(k string) string {
				if k == "GITSTACK_RAW_URL" {
					return raw.URL
				}
				return ""
			}
			a.HTTP = http.DefaultClient
			cfg := writeConfig(t, hostOf(srv), t.TempDir())
			if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitOK {
				t.Fatalf("exit %d\n%s\n%s", code, out, errb)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("manca %q in:\n%s", c.want, out)
			}
		})
	}
}

func TestStatusK3sJSON(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	raw := rawServer(t, "v1.36.5+k3s1")
	a, out, _ := newApp(&fakeRunner{}, srv)
	a.Runner = k3sStatusRunner{version: "v1.36.4+k3s1"}
	a.HTTP = http.DefaultClient
	a.Getenv = func(k string) string {
		if k == "GITSTACK_RAW_URL" {
			return raw.URL
		}
		return ""
	}
	cfg := writeConfig(t, hostOf(srv), t.TempDir())
	if code := a.Run(context.Background(), []string{"status", "--json", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"k3s_version": "v1.36.4+k3s1"`, `"k3s_declared": "v1.36.5+k3s1"`, `"k3s_update": "patch"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("manca %s in:\n%s", want, out)
		}
	}
}
