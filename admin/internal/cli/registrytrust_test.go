package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// trustRunner registra i comandi e fa risultare il nodo Ready.
type trustRunner struct{ calls []string }

func (r *trustRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "k3s" {
		return []byte("node1   Ready   control-plane   1d   v1\n"), nil
	}
	return nil, nil
}

func (r *trustRunner) restarts() int {
	n := 0
	for _, c := range r.calls {
		if c == "systemctl restart k3s" {
			n++
		}
	}
	return n
}

func trustApp(t *testing.T, r *trustRunner) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDir, "install.conf"), []byte("MODE=internal\nHOSTS=git.corp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDir, "ca.crt"), []byte("CA-VECCHIA"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newApp(&fakeRunner{}, nil)
	a.Runner = r
	k3sDir := filepath.Join(dir, "k3s")
	a.Getenv = func(k string) string {
		switch k {
		case "GITSTACK_K3S_DIR":
			return k3sDir
		case "GITSTACK_NETWORK_DIR":
			return filepath.Join(dir, "net")
		}
		return ""
	}
	return a, tlsDir, filepath.Join(k3sDir, "registries.yaml")
}

func TestRenderRegistriesCAInterna(t *testing.T) {
	trust := registryTrust{Hosts: []string{"git.corp", "172.27.25.76", "git.corp", " "}, CAFile: "/etc/gitstack/tls/ca.crt"}
	want := "# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.\n" +
		"configs:\n" +
		"  \"172.27.25.76\":\n    tls:\n      ca_file: \"/etc/gitstack/tls/ca.crt\"\n" +
		"  \"git.corp\":\n    tls:\n      ca_file: \"/etc/gitstack/tls/ca.crt\"\n"
	if got := renderRegistries(config.Network{}, "", trust); got != want {
		t.Errorf("senza mirror:\n%s\natteso:\n%s", got, want)
	}
	// Con mirror, CA aziendale e CA interna: una sola sezione configs, ciascun host una volta.
	n := config.Network{RegistryMirror: "https://reg.corp:5000", CABundle: "/etc/gitstack/network/ca.crt"}
	want = "# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.\n" +
		"mirrors:\n  \"*\":\n    endpoint:\n      - \"https://reg.corp:5000\"\n" +
		"configs:\n  \"reg.corp:5000\":\n    tls:\n      ca_file: \"/etc/gitstack/network/ca.crt\"\n" +
		"  \"172.27.25.76\":\n    tls:\n      ca_file: \"/etc/gitstack/tls/ca.crt\"\n" +
		"  \"git.corp\":\n    tls:\n      ca_file: \"/etc/gitstack/tls/ca.crt\"\n"
	if got := renderRegistries(n, n.CABundle, trust); got != want {
		t.Errorf("con mirror:\n%s\natteso:\n%s", got, want)
	}
	// Senza il file della CA non si scrive niente per il registry.
	if got := renderRegistries(config.Network{}, "", registryTrust{Hosts: []string{"git.corp"}}); got != "" {
		t.Errorf("senza CAFile: %q", got)
	}
}

func TestSyncRegistryTrust(t *testing.T) {
	r := &trustRunner{}
	a, tlsDir, regPath := trustApp(t, r)
	cfg := &config.Config{Host: "172.27.25.76", TLS: "internal"}
	ctx := context.Background()
	caBefore, _ := os.ReadFile(filepath.Join(tlsDir, "ca.crt"))

	// Primo giro: registries.yaml nasce con la CA interna per host e HOSTS, k3s si riavvia.
	if err := a.syncRegistryTrust(ctx, cfg, tlsDir, caBefore); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"172.27.25.76":`, `"git.corp":`, "ca_file: " + q(filepath.Join(tlsDir, "ca.crt"))} {
		if !strings.Contains(string(b), want) {
			t.Errorf("registries.yaml senza %s:\n%s", want, b)
		}
	}
	if r.restarts() != 1 {
		t.Fatalf("riavvii = %d, atteso 1: %v", r.restarts(), r.calls)
	}

	// Secondo giro, tutto uguale: nessun riavvio.
	if err := a.syncRegistryTrust(ctx, cfg, tlsDir, caBefore); err != nil {
		t.Fatal(err)
	}
	if r.restarts() != 1 {
		t.Fatalf("senza cambiamenti k3s non va riavviato: %v", r.calls)
	}

	// Restore: la CA sul disco è quella del backup, registries.yaml uguale: k3s si riavvia lo stesso.
	if err := os.WriteFile(filepath.Join(tlsDir, "ca.crt"), []byte("CA-DEL-BACKUP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.syncRegistryTrust(ctx, cfg, tlsDir, caBefore); err != nil {
		t.Fatal(err)
	}
	if r.restarts() != 2 {
		t.Fatalf("CA cambiata: k3s va riavviato: %v", r.calls)
	}

	// Host nuovo (config set host): la voce nasce e k3s si riavvia.
	if err := a.syncRegistryTrust(ctx, cfg, tlsDir, nil, "nuovo.corp"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(regPath); !strings.Contains(string(b), `"nuovo.corp":`) || r.restarts() != 3 {
		t.Fatalf("host nuovo: riavvii=%d\n%s", r.restarts(), b)
	}
}

func TestSyncRegistryTrustSoloConTLSInternal(t *testing.T) {
	for _, mode := range []string{"custom", "letsencrypt", "insecure", ""} {
		r := &trustRunner{}
		a, tlsDir, regPath := trustApp(t, r)
		if err := a.syncRegistryTrust(context.Background(), &config.Config{Host: "h", TLS: mode}, tlsDir, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(regPath); err == nil || len(r.calls) != 0 {
			t.Errorf("tls %q: registries.yaml scritto o k3s toccato: %v", mode, r.calls)
		}
	}
}
