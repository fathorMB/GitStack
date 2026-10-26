package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// withAppsChart fa conoscere al chart di prova l'hosting delle app (values.yaml con `apps:`).
func withAppsChart(t *testing.T, cfg string) {
	t.Helper()
	v := filepath.Join(filepath.Dir(cfg), "chart", "values.yaml")
	if err := os.WriteFile(v, []byte("global: {}\napps:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendConfig(t *testing.T, cfg, extra string) {
	t.Helper()
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(b, []byte(extra)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// GIT-375: `config set apps on` su una v2 senza dominio delle app prende il predefinito con avviso,
// rigenera il certificato wildcard, applica i valori al chart e salva la scelta.
func TestConfigSetAppsOnSenzaDominio(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	withAppsChart(t, cfg)
	r := &recRunner{getentOut: "192.168.1.20 x\n"}
	a, out, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps", "on", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb.String(), "--apps-domain non indicato") || !strings.Contains(errb.String(), "apps.gitstack.internal") {
		t.Errorf("manca l'avviso del dominio predefinito: %s", errb)
	}
	tls := r.find("gitstack-tls")
	if tls == nil || strings.Join(tls.args, " ") != "ensure --apps-domain apps.gitstack.internal" {
		t.Fatalf("gitstack-tls: %+v", tls)
	}
	h := r.find("helm")
	if h == nil {
		t.Fatal("helm upgrade non lanciato")
	}
	got := strings.Join(h.args, " ")
	for _, want := range []string{"upgrade gitstack", "--reuse-values", "--set apps.enabled=true", "--set apps.domain=apps.gitstack.internal"} {
		if !strings.Contains(got, want) {
			t.Errorf("helm senza %q: %s", want, got)
		}
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "apps_enabled: true\n") || !strings.Contains(string(b), "apps_domain: apps.gitstack.internal\n") {
		t.Errorf("config: %s", b)
	}
	ic, _ := os.ReadFile(filepath.Join(tlsDir, "install.conf"))
	if !strings.Contains(string(ic), "APPS_ENABLED=true") {
		t.Errorf("install.conf: %s", ic)
	}
	if !strings.Contains(out.String(), "Hosting delle app acceso") {
		t.Errorf("out: %s", out)
	}
	// Il config si rilegge (apps_enabled è una chiave nota).
	if c, err := config.Load(cfg); err != nil || !c.AppsEnabled {
		t.Errorf("config.Load: %v %+v", err, c)
	}
}

func TestConfigSetAppsOnUsaIlDominioEsistente(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	withAppsChart(t, cfg)
	appendConfig(t, cfg, "apps_domain: apps.azienda.it\n")
	r := &recRunner{}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps", "on", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if r.find("gitstack-tls") != nil {
		t.Error("il dominio non cambia: niente certificato nuovo")
	}
	if got := strings.Join(r.find("helm").args, " "); !strings.Contains(got, "--set apps.domain=apps.azienda.it") {
		t.Errorf("helm: %s", got)
	}
	if strings.Contains(errb.String(), "--apps-domain non indicato") {
		t.Errorf("avviso inatteso: %s", errb)
	}
}

func TestConfigSetAppsChartSenzaHosting(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal") // chart di prova senza values.yaml: una v2 non aggiornata
	r := &recRunner{}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps", "on", "--apps-domain", "apps.azienda.it", "--config", cfg}); code != ExitConfig {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb.String(), "gitstack upgrade --enable-apps") {
		t.Errorf("manca il rimando a upgrade: %s", errb)
	}
	if r.find("helm") != nil {
		t.Error("helm non doveva partire")
	}
}

func TestConfigSetAppsOff(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	withAppsChart(t, cfg)
	appendConfig(t, cfg, "apps_domain: apps.azienda.it\napps_enabled: true\n")
	r := &recRunner{}
	a, out, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps", "off", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := strings.Join(r.find("helm").args, " "); !strings.Contains(got, "--set apps.enabled=false") {
		t.Errorf("helm: %s", got)
	}
	after, _ := os.ReadFile(cfg)
	if !strings.Contains(string(after), "apps_enabled: false\n") || strings.Count(string(after), "apps_enabled:") != 1 {
		t.Errorf("config: %s", after)
	}
	if !strings.Contains(out.String(), "restano nel cluster") {
		t.Errorf("manca l'avviso sulle app esistenti: %s", out)
	}
	// Già spento: niente da fare.
	r2 := &recRunner{}
	a2, out2, _ := hostApp(r2, tlsDir)
	if code := a2.Run(context.Background(), []string{"config", "set", "apps", "off", "--config", cfg}); code != ExitOK || len(r2.calls) != 0 || !strings.Contains(out2.String(), "già spento") {
		t.Errorf("exit %d, chiamate %v, out %s", code, r2.calls, out2)
	}
}

func TestConfigSetAppsRejects(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	for _, args := range [][]string{
		{},
		{"forse"},
		{"on", "off"},
		{"on", "--auth-middleware", "default/login"}, // tolto: il forward-auth lo porta il chart
	} {
		r := &recRunner{}
		a, _, _ := hostApp(r, tlsDir)
		if code := a.Run(context.Background(), append(append([]string{"config", "set", "apps"}, args...), "--config", cfg)); code != ExitUsage {
			t.Errorf("%v: exit %d, atteso ExitUsage", args, code)
		}
		if len(r.calls) != 0 {
			t.Errorf("%v: comandi lanciati %v", args, r.calls)
		}
	}
}

// Con l'hosting acceso, cambiare il dominio delle app lo porta anche a core.
func TestConfigSetAppsDomainConHostingAccesoAggiornaIlChart(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	withAppsChart(t, cfg)
	appendConfig(t, cfg, "apps_domain: apps.azienda.it\napps_enabled: true\n")
	r := &recRunner{getentOut: "192.168.1.20 x\n"}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps-domain", "apps2.azienda.it", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	h := r.find("helm")
	if h == nil || !strings.Contains(strings.Join(h.args, " "), "--set apps.domain=apps2.azienda.it") {
		t.Fatalf("helm: %+v", h)
	}
}

func TestStatusDiceSeLHostingEAcceso(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    status.Report
		want string
	}{
		{"acceso", status.Report{AppsEnabled: true, AppsLive: "on"}, "Hosting delle app: acceso\n"},
		{"acceso, cluster non letto", status.Report{AppsEnabled: true}, "Hosting delle app: acceso\n"},
		{"spento", status.Report{}, "Hosting delle app: spento (gitstack config set apps on"},
		{"config acceso, core spento", status.Report{AppsEnabled: true, AppsLive: "off"}, "Hosting delle app: spento (config.yaml dice acceso"},
		{"core acceso, config spento", status.Report{AppsLive: "on"}, "Hosting delle app: acceso (ma config.yaml dice spento"},
	} {
		var sb strings.Builder
		printReport(&sb, "dev", &tc.r)
		if !strings.Contains(sb.String(), tc.want) {
			t.Errorf("%s: manca %q in:\n%s", tc.name, tc.want, sb.String())
		}
	}
}

// gitstack upgrade --enable-apps: i valori del chart e la chiave di config viaggiano con l'upgrade;
// in --dry-run non si tocca niente (nemmeno il dominio).
func TestUpgradeEnableAppsPassaValoriEConfig(t *testing.T) {
	a, cfg, _ := upgradeApp(t, "ahead")
	var got *upgrade.Options
	prev := a.TuneUpgrade
	a.TuneUpgrade = func(o *upgrade.Options) {
		prev(o)
		got = o
	}
	var out, errb strings.Builder
	a.Stdout, a.Stderr = &out, &errb
	_ = a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--dry-run", "--enable-apps"})
	if got == nil {
		t.Fatal("TuneUpgrade non chiamato")
	}
	have := strings.Join(got.Sets, " ")
	for _, want := range []string{"apps.enabled=true", "apps.domain=apps.h"} {
		if !strings.Contains(have, want) {
			t.Errorf("Sets senza %q: %s", want, have)
		}
	}
	if got.ExtraConfig["apps_enabled"] != "true" {
		t.Errorf("ExtraConfig: %v", got.ExtraConfig)
	}
	if !strings.Contains(errb.String(), "--apps-domain non indicato") {
		t.Errorf("manca l'avviso del dominio predefinito (v2 senza dominio): %s", errb.String())
	}
	b, _ := os.ReadFile(cfg)
	if strings.Contains(string(b), "apps_domain") || strings.Contains(string(b), "apps_enabled") {
		t.Errorf("--dry-run ha modificato il config: %s", b)
	}
}

func TestUpgradeSenzaEnableAppsNonTocca(t *testing.T) {
	a, cfg, _ := upgradeApp(t, "ahead")
	var got *upgrade.Options
	prev := a.TuneUpgrade
	a.TuneUpgrade = func(o *upgrade.Options) {
		prev(o)
		got = o
	}
	_ = a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--dry-run"})
	if got == nil || strings.Contains(strings.Join(got.Sets, " "), "apps.") || len(got.ExtraConfig) != 0 {
		t.Errorf("Options: %+v", got)
	}
	// --apps-domain vale solo con --enable-apps.
	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--apps-domain", "apps.x.it"}); code != ExitUsage {
		t.Errorf("--apps-domain senza --enable-apps: exit %d", code)
	}
}
