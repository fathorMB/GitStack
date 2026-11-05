package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	tlsOldScript = "#!/bin/sh\n# gitstack-tls dell'installazione: ensure --names --ips\n"
	tlsNewScript = "#!/bin/sh\n# gitstack-tls della release: ensure --apps-domain\n"
)

// tlsRunner finge helm e gitstack-tls: il tls "gira" leggendo lo script sul disco
// al momento della chiamata, e rifiuta --apps-domain se lo script non lo conosce.
type tlsRunner struct {
	recRunner
	tlsBin         string
	tlsRuns        []string
	helmUpgraded   bool
	helmRolledBack bool
	failRollback   bool
}

func (r *tlsRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	if name == r.tlsBin {
		r.tlsRuns = append(r.tlsRuns, strings.Join(args, " "))
		b, err := os.ReadFile(r.tlsBin)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.Join(args, " "), "--apps-domain") && !strings.Contains(string(b), "--apps-domain") {
			return []byte("ERRORE: opzione sconosciuta: --apps-domain\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if name == "k3s" && strings.Contains(strings.Join(args, " "), "get deployments") {
		return []byte(`{"items":[]}`), nil
	}
	if strings.HasSuffix(name, "helm") && len(args) > 0 {
		if args[0] == "upgrade" {
			r.helmUpgraded = true
		}
		if args[0] == "rollback" {
			r.helmRolledBack = true
			if r.failRollback {
				return []byte("rollback rifiutato"), errors.New("exit status 1")
			}
		}
		switch args[0] {
		case "history":
			return []byte(`[{"revision":2,"status":"deployed"}]`), nil
		case "get":
			return []byte("null"), nil
		case "template":
			return []byte("image: x/y:1\n"), nil
		}
		return nil, nil
	}
	return r.recRunner.Run(ctx, env, name, args...)
}

func tlsSourceTarball(t *testing.T, sha string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{
		{"deploy/gitstack-tls.sh", tlsNewScript},
		{"deploy/gitstack/Chart.yaml", "name: gitstack\nversion: 0.1.0\n"},
		{"deploy/gitstack/values.yaml", "NUOVO: 1\napps:\n  enabled: false\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: "GitStack-" + sha + "/" + f[0], Mode: 0o644, Size: int64(len(f[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(f[1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// GIT-543: dopo `gitstack upgrade` il gitstack-tls installato è quello della
// versione di destinazione, e `config set apps on --apps-domain` lo chiama.
func TestUpgradeAggiornaGitstackTLSEConfigSetAppsFunziona(t *testing.T) {
	const newBin = "BINARIO-NUOVO"
	sum := sha256.Sum256([]byte(newBin))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/commits/"):
			_, _ = io.WriteString(w, `{"sha":"`+cliNew+`"}`)
		case strings.Contains(p, "/compare/"):
			_, _ = io.WriteString(w, `{"status":"ahead"}`)
		case strings.HasSuffix(p, "/gitstack-linux-amd64.sha256"):
			_, _ = io.WriteString(w, hex.EncodeToString(sum[:])+"  gitstack-linux-amd64\n")
		case strings.HasSuffix(p, "/gitstack-linux-amd64"):
			_, _ = io.WriteString(w, newBin)
		case strings.HasSuffix(p, "/tar.gz/"+cliNew):
			_, _ = w.Write(tlsSourceTarball(t, cliNew))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	chart := filepath.Join(dir, "chart")
	tlsDir := filepath.Join(dir, "tls")
	tlsBin := filepath.Join(dir, "sbin", "gitstack-tls")
	exe := filepath.Join(dir, "bin", "gitstack")
	git := filepath.Join(dir, "git")
	for _, d := range []string{chart, tlsDir, filepath.Dir(tlsBin), filepath.Dir(exe), git} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		filepath.Join(chart, "Chart.yaml"):    "name: gitstack\n",
		filepath.Join(tlsDir, "tls.conf"):     "TLS_MODE=internal\nTLS_IPS=192.168.1.20\n",
		filepath.Join(tlsDir, "install.conf"): "MODE=internal\nHOSTS=192.168.1.20\nEMAIL=\n",
		exe:                                   "BINARIO-VECCHIO",
		filepath.Join(git, "f"):               "x",
	} {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(tlsBin, []byte(tlsOldScript), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	body := "version: 1\nhost: 192.168.1.20\nssh_port: 2222\ntls: internal\nchart_dir: " + filepath.ToSlash(chart) +
		"\nrelease: gitstack\nnamespace: default\nimage_tag: sha-" + cliOld + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &tlsRunner{tlsBin: tlsBin}
	a, _, _ := newApp(&fakeRunner{}, nil)
	var out, errb strings.Builder
	a.Stdout, a.Stderr = &out, &errb
	a.Runner = r
	a.Getenv = func(k string) string {
		switch k {
		case "GITSTACK_TLS_BIN":
			return tlsBin
		case "GITSTACK_TLS_DIR":
			return tlsDir
		case "GITSTACK_K3S_DIR":
			return filepath.Join(dir, "k3s")
		case "GITSTACK_NETWORK_DIR":
			return filepath.Join(dir, "net")
		}
		return ""
	}
	a.LocalAddrs = func() []string { return []string{"127.0.0.1", "192.168.1.20"} }
	a.ExePath = exe
	a.ExecBinary = func(context.Context, string, []string, io.Writer) error { return errors.New("no re-exec") }
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	a.TuneUpgrade = func(o *upgrade.Options) {
		o.Backup.DestDir = filepath.Join(dir, "backups")
		o.APIBase, o.CodeloadURL, o.ReleaseURL = srv.URL, srv.URL, srv.URL
		o.Poll = 10 * time.Millisecond
		o.CheckCA = nil
		o.ImageCheck = func(context.Context, string) error { return nil }
		o.Health = func(context.Context) *status.Report {
			return &status.Report{APIHealthy: true, Services: []status.Service{{Name: "s", Desired: 1, Ready: 1}}}
		}
	}
	apps := []string{"config", "set", "apps", "on", "--apps-domain", "apps.homehub.lan", "--config", cfg}

	// Prima: il tls dell'installazione non conosce --apps-domain (il caso di homehub).
	if code := a.Run(context.Background(), apps); code == ExitOK {
		t.Fatal("config set apps con il tls vecchio doveva fallire")
	}
	if !strings.Contains(errb.String(), "opzione sconosciuta: --apps-domain") {
		t.Fatalf("errore inatteso: %s", errb.String())
	}

	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--allow-unsigned-mirrors"}); code != ExitOK {
		t.Fatalf("upgrade: exit %d\nout: %s\nerr: %s", code, out.String(), errb.String())
	}
	if got, _ := os.ReadFile(tlsBin); string(got) != tlsNewScript {
		t.Fatalf("gitstack-tls dopo l'upgrade: %q", got)
	}
	if !strings.Contains(out.String(), "gitstack-tls aggiornato") {
		t.Errorf("l'output non dice che gitstack-tls è stato aggiornato: %s", out.String())
	}

	// Dopo: lo stesso comando arriva al tls nuovo.
	errb.Reset()
	r.tlsRuns = nil
	if code := a.Run(context.Background(), apps); code != ExitOK {
		t.Fatalf("config set apps dopo l'upgrade: exit %d, stderr: %s", code, errb.String())
	}
	if len(r.tlsRuns) == 0 || !strings.Contains(r.tlsRuns[len(r.tlsRuns)-1], "--apps-domain apps.homehub.lan") {
		t.Errorf("gitstack-tls non chiamato con --apps-domain: %v", r.tlsRuns)
	}
}
