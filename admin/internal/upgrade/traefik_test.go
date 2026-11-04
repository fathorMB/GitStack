package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/traefikcfg"
)

func summary(res *Result) string { return strings.Join(res.Summary, "\n") }

// GIT-534: installazione precedente a GIT-403, senza gitstack-traefik.yaml.
func TestUpgradeScriveTraefikSeAssente(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|6|f\ncore|12|f"
	if _, err := os.Stat(w.o.TraefikManifest); err == nil {
		t.Fatal("il file non doveva esistere")
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got := readFile(t, w.o.TraefikManifest)
	for _, s := range []string{"kind: HelmChartConfig", "name: traefik", "namespace: kube-system", "entryPoint: metrics", "port: 9100"} {
		if !strings.Contains(got, s) {
			t.Errorf("manca %q:\n%s", s, got)
		}
	}
	if strings.Contains(got, "certificatesResolvers") || strings.Contains(got, "cigateway") {
		t.Errorf("TLS interno e gateway spento: niente resolver né porta\n%s", got)
	}
	if !strings.Contains(summary(res), "scritta la HelmChartConfig") {
		t.Errorf("riepilogo: %s", summary(res))
	}
}

func TestUpgradeTraefikLetsEncryptDaConfig(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|6|f\ncore|12|f"
	w.o.Cfg.TLS = "letsencrypt"
	mustWriteMode(t, filepath.Join(filepath.Dir(w.cfgFile), "tls", "install.conf"), 0o600, "MODE=letsencrypt\nEMAIL=ops@example.com\n")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got := readFile(t, w.o.TraefikManifest)
	for _, s := range []string{"certificatesResolvers:", "letsencrypt:", "email: ops@example.com", "storage: /data/acme.json"} {
		if !strings.Contains(got, s) {
			t.Errorf("manca %q:\n%s", s, got)
		}
	}
}

func TestUpgradeTraefikConservaAcmeEGateway(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|6|f\ncore|12|f"
	// File di un'installazione recente (ACME e gateway acceso) ma senza le metriche attese.
	old, err := traefikcfg.Render(traefikcfg.Params{MetricsValues: []byte("ports:\n  web:\n    port: 80\n"), CIGatewayPort: 9443,
		ACME: &traefikcfg.ACME{Resolver: "letsencrypt", Email: "a@example.com", CAServer: "https://pebble:14000/dir"}})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteMode(t, w.o.TraefikManifest, 0o644, string(old))
	legacy := filepath.Join(filepath.Dir(w.o.TraefikManifest), traefikcfg.LegacyACMEName)
	mustWriteMode(t, legacy, 0o644, "vecchio")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got := readFile(t, w.o.TraefikManifest)
	for _, s := range []string{"entryPoint: metrics", "exposedPort: 9443", "email: a@example.com", "caServer: https://pebble:14000/dir"} {
		if !strings.Contains(got, s) {
			t.Errorf("manca %q:\n%s", s, got)
		}
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("il vecchio file del solo Let's Encrypt doveva sparire: %v", err)
	}
}

func TestDryRunMostraManifestTraefik(t *testing.T) {
	w := newWorld(t)
	w.o.DryRun = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(summary(res), "Traefik: scriverei") {
		t.Errorf("il dry-run non dice cosa scriverebbe: %s", summary(res))
	}
	if _, err := os.Stat(w.o.TraefikManifest); err == nil {
		t.Error("dry-run ha scritto il manifest")
	}
}

// Già alla versione richiesta (come homehub dopo un upgrade a metà): il manifest si scrive lo stesso.
func TestVersioneIdenticaScriveTraefik(t *testing.T) {
	w := newWorld(t)
	w.o.To = "sha-" + newSHA
	w.o.Cfg.ImageTag = "sha-" + newSHA
	mustWrite(t, filepath.Join(w.chart, "files", "traefik-metrics-values.yaml"), "metrics:\n  prometheus:\n    entryPoint: metrics\n")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.o.TraefikManifest); !strings.Contains(got, "entryPoint: metrics") {
		t.Errorf("manifest: %s", got)
	}
}
