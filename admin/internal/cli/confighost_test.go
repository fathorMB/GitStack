package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
	env  []string
}

// recRunner registra ogni comando; getent risponde con getentOut/getentErr.
type recRunner struct {
	calls     []call
	getentOut string
	getentErr error
	helmErr   error
}

func (r *recRunner) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name, args, env})
	switch {
	case name == "getent":
		return []byte(r.getentOut), r.getentErr
	case strings.HasSuffix(name, "helm"):
		return nil, r.helmErr
	}
	return nil, nil
}

func (r *recRunner) find(suffix string) *call {
	for i := range r.calls {
		if strings.HasSuffix(r.calls[i].name, suffix) {
			return &r.calls[i]
		}
	}
	return nil
}

// hostFixture prepara config.yaml, chart e tls dir in una cartella temporanea.
func hostFixture(t *testing.T, tlsMode string) (cfgPath, tlsDir string) {
	t.Helper()
	dir := t.TempDir()
	chart := filepath.Join(dir, "chart")
	tlsDir = filepath.Join(dir, "tls")
	for _, d := range []string{chart, tlsDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("name: gitstack\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDir, "tls.conf"), []byte("TLS_MODE=internal\nTLS_IPS=192.168.1.20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDir, "install.conf"), []byte("MODE=internal\nHOSTS=192.168.1.20\nEMAIL=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "config.yaml")
	body := "# commento\nversion: 1\nhost: 192.168.1.20\nssh_port: 2222\ntls: " + tlsMode + "\nchart_dir: " + filepath.ToSlash(chart) + "\nrelease: gitstack\nnamespace: default\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, tlsDir
}

func hostApp(r *recRunner, tlsDir string) (*App, *strings.Builder, *strings.Builder) {
	a, _, _ := newApp(&fakeRunner{}, nil)
	var out, errb strings.Builder
	a.Stdout, a.Stderr = &out, &errb
	a.Runner = r
	a.Getenv = func(k string) string {
		if k == "GITSTACK_TLS_DIR" {
			return tlsDir
		}
		return ""
	}
	a.LocalAddrs = func() []string { return []string{"127.0.0.1", "192.168.1.20"} }
	return a, &out, &errb
}

func TestConfigSetHostLocalName(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{getentOut: "192.168.1.20   homehub.local\n"}
	a, out, errb := hostApp(r, tlsDir)

	if code := a.Run(context.Background(), []string{"config", "set", "host", "HomeHub.local", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	tls := r.find("gitstack-tls")
	if tls == nil || strings.Join(tls.args, " ") != "ensure --names homehub.local --ips 192.168.1.20" {
		t.Fatalf("gitstack-tls: %+v", tls)
	}
	helm := r.find("helm")
	if helm == nil {
		t.Fatal("helm non eseguito")
	}
	j := strings.Join(helm.args, " ")
	for _, want := range []string{"--reuse-values", "core.env.publicUrl=https://homehub.local", "identity.oidc.publicUrl=https://homehub.local"} {
		if !strings.Contains(j, want) {
			t.Errorf("helm senza %q: %s", want, j)
		}
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "host: homehub.local\n") || !strings.Contains(string(b), "# commento") {
		t.Errorf("config: %s", b)
	}
	ic, _ := os.ReadFile(filepath.Join(tlsDir, "install.conf"))
	if !strings.Contains(string(ic), "HOSTS=homehub.local") {
		t.Errorf("install.conf: %s", ic)
	}
	if !strings.Contains(out.String(), "indirizzi di clone") {
		t.Errorf("manca l'avviso su clone ed email: %s", out)
	}
	if !strings.Contains(errb.String(), "mDNS") || strings.Contains(errb.String(), "non risolve") {
		t.Errorf("avvisi inattesi: %s", errb)
	}
}

func TestConfigSetHostNotResolving(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{getentErr: errors.New("exit status 2")}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "git.example.test", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb.String(), "non risolve") {
		t.Errorf("manca l'avviso: %s", errb)
	}
}

func TestConfigSetHostResolvesElsewhere(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{getentOut: "10.9.9.9 altro.example.test\n"}
	a, _, errb := hostApp(r, tlsDir)
	a.Run(context.Background(), []string{"config", "set", "host", "altro.example.test", "--config", cfg})
	if !strings.Contains(errb.String(), "non a un indirizzo di questa macchina") {
		t.Errorf("manca l'avviso: %s", errb)
	}
}

func TestConfigSetHostIPAddsSAN(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "10.0.0.5", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := strings.Join(r.find("gitstack-tls").args, " "); got != "ensure --names  --ips 192.168.1.20,10.0.0.5" {
		t.Errorf("args: %q", got)
	}
	if len(r.calls) != 2 { // niente getent per un IP
		t.Errorf("chiamate: %+v", r.calls)
	}
}

func TestConfigSetHostInsecureSkipsCert(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "insecure")
	r := &recRunner{getentOut: "192.168.1.20 x.local\n"}
	a, _, _ := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "x.local", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if r.find("gitstack-tls") != nil {
		t.Error("gitstack-tls non va chiamato con tls insecure")
	}
	if !strings.Contains(strings.Join(r.find("helm").args, " "), "publicUrl=http://x.local") {
		t.Errorf("schema: %+v", r.find("helm"))
	}
}

func TestConfigSetHostErrors(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	for _, bad := range []string{"http://x", "a b", "x/y", "-x", "x:443"} {
		a, _, _ := hostApp(&recRunner{}, tlsDir)
		if code := a.Run(context.Background(), []string{"config", "set", "host", bad, "--config", cfg}); code != ExitUsage {
			t.Errorf("%q: exit %d, atteso %d", bad, code, ExitUsage)
		}
	}
	a, _, _ := hostApp(&recRunner{}, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "--config", cfg}); code != ExitUsage {
		t.Errorf("senza nome: exit %d", code)
	}
	if code := a.Run(context.Background(), []string{"config", "get", "host"}); code != ExitUsage {
		t.Errorf("config get: exit %d", code)
	}
	// helm fallisce: la configurazione non cambia.
	r := &recRunner{getentOut: "192.168.1.20 y.local\n", helmErr: errors.New("boom")}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "y.local", "--config", cfg}); code != ExitCluster {
		t.Fatalf("helm ko: exit %d", code)
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), "host: 192.168.1.20") {
		t.Errorf("config cambiata dopo un errore: %s", b)
	}
	if !strings.Contains(errb.String(), "rilancia") {
		t.Errorf("manca l'indicazione: %s", errb)
	}
}

func TestConfigSetHostLetsEncryptRejectsLocal(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "letsencrypt")
	a, _, _ := hostApp(&recRunner{}, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "host", "x.local", "--config", cfg}); code != ExitConfig {
		t.Errorf("exit %d", code)
	}
}

func TestConfigSetHostNeedsRoot(t *testing.T) {
	a, _, _ := newApp(&fakeRunner{}, nil)
	a.Geteuid = func() int { return 1000 }
	if code := a.Run(context.Background(), []string{"config", "set", "host", "x.local"}); code != ExitNeedsRoot {
		t.Errorf("exit %d", code)
	}
}
