package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type gatewayRunner struct {
	calls       [][]string
	failUpgrade bool
}

func (r *gatewayRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	if args[0] == "get" {
		return []byte(`{"ci":{"enabled":true}}`), nil
	}
	if r.failUpgrade {
		return nil, fmt.Errorf("helm fallito")
	}
	return nil, nil
}

func TestCIGatewayOnOff(t *testing.T) {
	cfg, tls := hostFixture(t, "internal")
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "chart", "values.yaml"), []byte("ci:\n  externalGateway:\n    publicPort: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(filepath.Dir(cfg), "traefik.yaml")
	original := "apiVersion: helm.cattle.io/v1\nkind: HelmChartConfig\nmetadata:\n  name: traefik\n  namespace: kube-system\nspec:\n  valuesContent: |-\n    ports:\n      metrics:\n        port: 9100\n    certificatesResolvers:\n      letsencrypt: {}\n"
	if err := os.WriteFile(manifest, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _, errs := hostApp(&recRunner{}, tls)
	r := &gatewayRunner{}
	a.Runner = r
	oldGetenv := a.Getenv
	a.Getenv = func(k string) string {
		if k == "GITSTACK_TRAEFIK_MANIFEST" {
			return manifest
		}
		return oldGetenv(k)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if code := a.Run(context.Background(), []string{"config", "set", "ci-gateway", "on", "--port", fmt.Sprint(port), "--config", cfg}); code != ExitOK {
		t.Fatalf("on: %d %s", code, errs)
	}
	b, _ := os.ReadFile(manifest)
	for _, s := range []string{"cigateway:", "metrics:", "certificatesResolvers:", "port: 18443", fmt.Sprintf("exposedPort: %d", port)} {
		if !strings.Contains(string(b), s) {
			t.Errorf("manifest senza %s: %s", s, b)
		}
	}
	b, _ = os.ReadFile(cfg)
	if !strings.Contains(string(b), "ci_gateway_enabled: true") {
		t.Errorf("config: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(tls, "install.conf"))
	if !strings.Contains(string(b), "CI_GATEWAY_ENABLED=true") {
		t.Errorf("install.conf: %s", b)
	}
	if len(r.calls) != 2 || !strings.Contains(strings.Join(r.calls[1], " "), "ci.externalGateway.publicPort="+fmt.Sprint(port)) {
		t.Fatalf("helm: %v", r.calls)
	}
	if code := a.Run(context.Background(), []string{"config", "set", "ci-gateway", "off", "--config", cfg}); code != ExitOK {
		t.Fatalf("off: %d %s", code, errs)
	}
	b, _ = os.ReadFile(manifest)
	if strings.Contains(string(b), "cigateway:") || !strings.Contains(string(b), "certificatesResolvers:") {
		t.Errorf("manifest off: %s", b)
	}
}

func TestCIGatewayManifestRejectsWrongObject(t *testing.T) {
	if _, err := gatewayManifest([]byte("kind: ConfigMap\nmetadata:\n  name: traefik\n"), true, 8443); err == nil {
		t.Fatal("manifest errato accettato")
	}
}

func TestCIGatewayRejectsOldChartWithPostgresPublicPort(t *testing.T) {
	cfg, tls := hostFixture(t, "internal")
	values := "postgres:\n  external:\n    publicPort: 8443\nci:\n  externalGateway:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "chart", "values.yaml"), []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _, errs := hostApp(&recRunner{}, tls)
	if code := a.Run(context.Background(), []string{"config", "set", "ci-gateway", "off", "--config", cfg}); code != ExitConfig {
		t.Fatalf("codice %d, atteso ExitConfig: %s", code, errs)
	}
}
