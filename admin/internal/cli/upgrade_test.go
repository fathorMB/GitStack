package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

const (
	cliOld = "1111111111111111111111111111111111111111"
	cliNew = "2222222222222222222222222222222222222222"
)

type helmOnly struct{ calls []string }

func (h *helmOnly) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	h.calls = append(h.calls, strings.Join(args, " "))
	switch args[0] {
	case "history":
		return []byte(`[{"revision":2,"status":"deployed"}]`), nil
	case "get":
		return []byte("null"), nil
	case "template":
		return []byte("image: x/y:1\n"), nil
	case "upgrade":
		return nil, errors.New("exit 1")
	}
	return nil, nil
}

func upgradeApp(t *testing.T, cmp string) (*App, string, *helmOnly) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = io.WriteString(w, `{"sha":"`+cliNew+`"}`)
		case strings.Contains(r.URL.Path, "/compare/"):
			_, _ = io.WriteString(w, `{"status":"`+cmp+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, _, _ := newApp(&fakeRunner{}, nil)
	h := &helmOnly{}
	a.Runner = h
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	a.TuneUpgrade = func(o *upgrade.Options) {
		o.APIBase, o.CodeloadURL, o.ReleaseURL = srv.URL, srv.URL, srv.URL
		o.Poll = 10 * time.Millisecond
		o.ImageCheck = func(context.Context, string) error { return nil }
		o.Health = func(context.Context) *status.Report {
			return &status.Report{APIHealthy: true, Services: []status.Service{{Name: "s", Desired: 1, Ready: 1}}}
		}
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("version: 1\nhost: h\nimage_tag: sha-"+cliOld+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, cfg, h
}

func TestUpgradeDowngradeRefused(t *testing.T) {
	a, cfg, h := upgradeApp(t, "behind")
	var stderr strings.Builder
	a.Stderr = &stderr
	code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--dest", t.TempDir(), "--to", cliNew})
	if code != ExitRefused {
		t.Fatalf("exit %d, atteso %d", code, ExitRefused)
	}
	if !strings.Contains(stderr.String(), "downgrade non supportato") || !strings.Contains(stderr.String(), "Niente è stato modificato") {
		t.Errorf("messaggio: %s", stderr.String())
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "upgrade ") {
			t.Errorf("helm upgrade lanciato: %s", c)
		}
	}
}

func TestUpgradeUsageAndRoot(t *testing.T) {
	a, cfg, _ := upgradeApp(t, "ahead")
	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "extra"}); code != ExitUsage {
		t.Errorf("argomento extra: %d", code)
	}
	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--timeout", "5s"}); code != ExitUsage {
		t.Errorf("timeout corto: %d", code)
	}
	a.Geteuid = func() int { return 1000 }
	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg}); code != ExitNeedsRoot {
		t.Errorf("senza root: %d", code)
	}
}

// Binario e chart non pubblicati: rifiutato prima di toccare qualcosa.
func TestUpgradeMissingAssetsRefused(t *testing.T) {
	a, cfg, _ := upgradeApp(t, "ahead")
	// serve un binario e un chart scaricabili: senza, l'aggiornamento è
	// rifiutato prima. Qui basta verificare che il rifiuto sia ExitRefused.
	code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--dest", t.TempDir(), "--dry-run"})
	if code != ExitRefused {
		t.Errorf("binario non pubblicato: exit %d", code)
	}
}
