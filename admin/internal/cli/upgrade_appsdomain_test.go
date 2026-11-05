package cli

import (
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

// GIT-559: il dominio delle app si applica dopo upgrade.Run, con lo script gitstack-tls della destinazione.

type domEnv struct {
	a      *App
	r      *tlsRunner
	out    *strings.Builder
	errb   *strings.Builder
	cfg    string
	tlsBin string
}

// newDomEnv prepara un host con gitstack-tls vecchio e image_tag = installed.
func newDomEnv(t *testing.T, installed string, tune func(*upgrade.Options)) *domEnv {
	t.Helper()
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
		filepath.Join(chart, "values.yaml"):   "apps:\n  enabled: false\n",
		filepath.Join(tlsDir, "tls.conf"):     "TLS_MODE=internal\nTLS_IPS=192.168.1.20\n",
		filepath.Join(tlsDir, "install.conf"): "MODE=internal\nHOSTS=192.168.1.20\nEMAIL=\n",
		exe:                                   "BINARIO-VECCHIO",
		filepath.Join(git, "f"):               "x",
		tlsBin:                                tlsOldScript,
	} {
		if err := os.WriteFile(p, []byte(c), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(dir, "config.yaml")
	body := "version: 1\nhost: 192.168.1.20\nssh_port: 2222\ntls: internal\nchart_dir: " + filepath.ToSlash(chart) +
		"\nrelease: gitstack\nnamespace: default\nimage_tag: sha-" + installed + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &tlsRunner{tlsBin: tlsBin}
	a, _, _ := newApp(&fakeRunner{}, nil)
	out, errb := &strings.Builder{}, &strings.Builder{}
	a.Stdout, a.Stderr = out, errb
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
		o.APIBase, o.CodeloadURL, o.ReleaseURL = srv.URL, srv.URL, srv.URL
		o.Poll = 10 * time.Millisecond
		o.CheckCA = nil
		o.Backup.DestDir = filepath.Join(dir, "backups")
		o.ImageCheck = func(context.Context, string) error { return nil }
		o.Health = func(context.Context) *status.Report {
			return &status.Report{APIHealthy: true, Services: []status.Service{{Name: "s", Desired: 1, Ready: 1}}}
		}
		if tune != nil {
			tune(o)
		}
	}
	return &domEnv{a: a, r: r, out: out, errb: errb, cfg: cfg, tlsBin: tlsBin}
}

func (e *domEnv) config(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var domArgs = []string{"--enable-apps", "--apps-domain", "apps.example.lan", "--allow-unsigned-mirrors"}

func (e *domEnv) upgrade(extra ...string) int {
	args := append([]string{"upgrade", "--config", e.cfg}, domArgs...)
	return e.a.Run(context.Background(), append(args, extra...))
}

func TestUpgradeEnableAppsDomainConTLSVecchioApplicaDopoLoScriptNuovo(t *testing.T) {
	e := newDomEnv(t, cliOld, nil)
	if code := e.upgrade(); code != ExitOK {
		t.Fatalf("exit %d\nout: %s\nerr: %s", code, e.out, e.errb)
	}
	if got, _ := os.ReadFile(e.tlsBin); string(got) != tlsNewScript {
		t.Fatalf("gitstack-tls dopo l'upgrade: %q", got)
	}
	// tlsRunner rifiuta --apps-domain se lo script sul disco è il vecchio: la chiamata riuscita prova l'ordine.
	if len(e.r.tlsRuns) != 1 || !strings.Contains(e.r.tlsRuns[0], "--apps-domain apps.example.lan") {
		t.Fatalf("gitstack-tls chiamato così: %v", e.r.tlsRuns)
	}
	c := e.config(t)
	if c.AppsDomain != "apps.example.lan" || !c.AppsEnabled {
		t.Errorf("config: apps_domain=%q apps_enabled=%v", c.AppsDomain, c.AppsEnabled)
	}
	if !strings.Contains(e.out.String(), "Hosting delle app acceso") {
		t.Errorf("output: %s", e.out)
	}
}

func TestUpgradeEnableAppsDomainRifiutatoNonApplicaIlDominio(t *testing.T) {
	e := newDomEnv(t, cliOld, func(o *upgrade.Options) {
		o.ImageCheck = func(context.Context, string) error { return errors.New("immagine assente") }
	})
	if code := e.upgrade(); code != ExitRefused {
		t.Fatalf("exit %d (atteso %d)\nerr: %s", code, ExitRefused, e.errb)
	}
	if len(e.r.tlsRuns) != 0 {
		t.Errorf("gitstack-tls non doveva essere chiamato: %v", e.r.tlsRuns)
	}
	if c := e.config(t); c.AppsDomain != "" {
		t.Errorf("apps_domain impostato a metà: %q", c.AppsDomain)
	}
	if s := e.errb.String(); !strings.Contains(s, "NON è stato applicato") || !strings.Contains(s, "gitstack config set apps-domain apps.example.lan") {
		t.Errorf("l'output non dice stato e comando: %s", s)
	}
}

// rollbackEnv prepara un upgrade che fallisce dopo l'helm upgrade, con un apps_domain precedente in config.yaml.
// healthyAfterRollback: la salute torna sana dopo l'helm rollback (RolledBack); failRollback: l'helm rollback
// stesso fallisce (Broken).
func rollbackEnv(t *testing.T, healthyAfterRollback, failRollback bool) *domEnv {
	t.Helper()
	var e *domEnv
	e = newDomEnv(t, cliOld, func(o *upgrade.Options) {
		o.Timeout = 300 * time.Millisecond
		o.Health = func(context.Context) *status.Report {
			if !e.r.helmUpgraded || (healthyAfterRollback && e.r.helmRolledBack) {
				return &status.Report{APIHealthy: true, Services: []status.Service{{Name: "s", Desired: 1, Ready: 1}}}
			}
			return &status.Report{Services: []status.Service{{Name: "s", Desired: 1, Ready: 0}}}
		}
	})
	e.r.failRollback = failRollback
	f, err := os.OpenFile(e.cfg, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("apps_domain: apps.old.lan\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return e
}

func assertDominioNonApplicato(t *testing.T, e *domEnv) {
	t.Helper()
	if len(e.r.tlsRuns) != 0 {
		t.Errorf("gitstack-tls non doveva essere chiamato: %v", e.r.tlsRuns)
	}
	if c := e.config(t); c.AppsDomain != "apps.old.lan" {
		t.Errorf("apps_domain precedente non conservato: %q", c.AppsDomain)
	}
	s := e.errb.String()
	for _, want := range []string{
		"Il dominio delle app NON è stato applicato (resta \"apps.old.lan\")",
		"gitstack upgrade --enable-apps --apps-domain apps.example.lan",
		"gitstack config set apps-domain apps.example.lan",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manca %q in: %s", want, s)
		}
	}
}

// GIT-559, criterio 2: rollback riuscito (la salute torna sana dopo l'helm rollback) = esattamente ExitRolledBack.
func TestUpgradeEnableAppsDomainRolledBackNonApplicaIlDominio(t *testing.T) {
	e := rollbackEnv(t, true, false)
	if code := e.upgrade(); code != ExitRolledBack {
		t.Fatalf("exit %d (atteso %d, ExitRolledBack)\nout: %s\nerr: %s", code, ExitRolledBack, e.out, e.errb)
	}
	if !e.r.helmRolledBack {
		t.Error("helm rollback non eseguito")
	}
	assertDominioNonApplicato(t, e)
	if s := e.errb.String(); !strings.Contains(s, "Rollback riuscito") {
		t.Errorf("l'output non dice che è RolledBack: %s", s)
	}
}

// GIT-559, criterio 2: rollback fallito (helm rollback in errore) = esattamente ExitBroken.
func TestUpgradeEnableAppsDomainBrokenNonApplicaIlDominio(t *testing.T) {
	e := rollbackEnv(t, false, true)
	if code := e.upgrade(); code != ExitBroken {
		t.Fatalf("exit %d (atteso %d, ExitBroken)\nout: %s\nerr: %s", code, ExitBroken, e.out, e.errb)
	}
	assertDominioNonApplicato(t, e)
	if s := e.errb.String(); !strings.Contains(s, "rollback NON riuscito") || !strings.Contains(s, "helm rollback gitstack") {
		t.Errorf("l'output non dice che è Broken e come recuperare: %s", s)
	}
}

// GIT-559, criterio 2: la salute non torna sana neanche dopo il rollback = Broken (nessun helm rollback in errore).
func TestUpgradeEnableAppsDomainSaluteNonTornaNonApplicaIlDominio(t *testing.T) {
	e := rollbackEnv(t, false, false)
	if code := e.upgrade(); code != ExitBroken {
		t.Fatalf("exit %d (atteso %d, ExitBroken)\nout: %s\nerr: %s", code, ExitBroken, e.out, e.errb)
	}
	assertDominioNonApplicato(t, e)
	if s := e.errb.String(); !strings.Contains(s, "rollback NON riuscito") {
		t.Errorf("l'output non dice che è Broken: %s", s)
	}
}

func TestUpgradeEnableAppsDomainNoChangeConTLSVecchioDiceCosaFare(t *testing.T) {
	e := newDomEnv(t, cliNew, nil)
	code := e.upgrade()
	if code == ExitOK {
		t.Fatalf("con lo script vecchio e nessun upgrade doveva fallire\nout: %s", e.out)
	}
	s := e.errb.String()
	for _, want := range []string{"opzione sconosciuta", "gitstack-tls installato è più vecchio del binario", "sudo gitstack upgrade", "deploy/gitstack-tls.sh", "gitstack config set apps-domain apps.example.lan"} {
		if !strings.Contains(s, want) {
			t.Errorf("manca %q in: %s", want, s)
		}
	}
	if c := e.config(t); c.AppsDomain != "" {
		t.Errorf("apps_domain impostato a metà: %q", c.AppsDomain)
	}
}

func TestConfigSetAppsConTLSVecchioStampaIlSuggerimento(t *testing.T) {
	e := newDomEnv(t, cliNew, nil)
	for _, args := range [][]string{
		{"config", "set", "apps", "on", "--apps-domain", "apps.example.lan", "--config", e.cfg},
		{"config", "set", "apps-domain", "apps.example.lan", "--config", e.cfg},
	} {
		e.errb.Reset()
		if code := e.a.Run(context.Background(), args); code == ExitOK {
			t.Fatalf("%v doveva fallire", args)
		}
		if !strings.Contains(e.errb.String(), "gitstack-tls installato è più vecchio del binario: lancia di nuovo `sudo gitstack upgrade` o reinstalla deploy/gitstack-tls.sh") {
			t.Errorf("%v: manca il suggerimento: %s", args, e.errb)
		}
	}
}
