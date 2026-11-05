package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/sysinfo"
)

// Database gestiti (GIT-557, ADR 0003, DB23): `gitstack config set databases on|off` e
// `gitstack upgrade --enable-databases`. Ogni test scrive solo in t.TempDir().

const gbytes = int64(1_000_000_000)

// withDatabasesChart fa conoscere al chart di prova i database gestiti (values.yaml con `databases:`).
func withDatabasesChart(t *testing.T, cfg string) {
	t.Helper()
	v := filepath.Join(filepath.Dir(cfg), "chart", "values.yaml")
	if err := os.WriteFile(v, []byte("global: {}\napps:\n  enabled: false\ndatabases:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bigHost() sysinfo.NodeProbe {
	return sysinfo.NodeProbe{CPUCores: 8, MemoryBytes: 16 * gbytes, DiskPath: "/", DiskTotalBytes: 500 * gbytes, DiskFreeBytes: 200 * gbytes}
}

// dbEnv è un host con chart, radice dell'istanza e un cluster che tiene i Secret.
type dbEnv struct {
	cfg, tlsDir string
	r           *recRunner
	a           *App
	out, errb   *strings.Builder
	cl          *secretsCluster
}

func newDBEnv(t *testing.T) *dbEnv {
	t.Helper()
	cfg, tlsDir := hostFixture(t, "internal")
	withDatabasesChart(t, cfg)
	writeRootCA(t, tlsDir)
	r := &recRunner{}
	a, out, errb := hostApp(r, tlsDir)
	cl := &secretsCluster{}
	a.NewCluster = func(*config.Config) backup.Cluster { return cl }
	a.NodeProbe = bigHost
	return &dbEnv{cfg: cfg, tlsDir: tlsDir, r: r, a: a, out: out, errb: errb, cl: cl}
}

func (e *dbEnv) config(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *dbEnv) installConf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.tlsDir, "install.conf"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e *dbEnv) run(args ...string) int {
	return e.a.Run(context.Background(), append(args, "--config", e.cfg))
}

func helmLines(r *recRunner) []string {
	var out []string
	for _, c := range r.calls {
		if strings.HasSuffix(c.name, "helm") {
			out = append(out, strings.Join(c.args, " "))
		}
	}
	return out
}

// Accensione su un'installazione esistente: crea CA e chiave, tocca SOLO databases.* del chart (niente app,
// repo o Postgres), salva la scelta e ricorda il firewall.
func TestConfigSetDatabasesOn(t *testing.T) {
	e := newDBEnv(t)
	if code := e.run("config", "set", "databases", "on", "--databases-port-range", "20000-20010"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, e.errb)
	}
	if len(e.cl.secrets) != 2 {
		t.Errorf("attesi CA intermedia e chiave di dump, trovati %d Secret", len(e.cl.secrets))
	}
	h := helmLines(e.r)
	if len(h) != 1 {
		t.Fatalf("helm chiamato %d volte: %v", len(h), h)
	}
	for _, want := range []string{"upgrade gitstack", "--reuse-values", "--set databases.enabled=true", "--set databases.lan.portRange.from=20000", "--set databases.lan.portRange.to=20010"} {
		if !strings.Contains(h[0], want) {
			t.Errorf("helm senza %q: %s", want, h[0])
		}
	}
	for _, not := range []string{"apps.", "git.", "postgres.", "ci."} {
		if strings.Contains(h[0], "--set "+not) {
			t.Errorf("helm tocca %q, che non c'entra: %s", not, h[0])
		}
	}
	c := e.config(t)
	if !c.DatabasesEnabled || c.DatabasesPortFrom != 20000 || c.DatabasesPortTo != 20010 {
		t.Errorf("config: %+v", c)
	}
	ic := e.installConf(t)
	if !strings.Contains(ic, "DATABASES_ENABLED=true") || !strings.Contains(ic, "DATABASES_PORT_RANGE=20000-20010") {
		t.Errorf("install.conf: %s", ic)
	}
	if !strings.Contains(e.out.String(), "ufw allow from <rete> to any port 20000:20010 proto tcp") {
		t.Errorf("manca il promemoria del firewall: %s", e.out)
	}
}

// Spegnere non cancella: una sola chiamata a helm con databases.enabled=false, nessun comando che tocchi
// namespace, volumi o Secret, Secret della CA e della chiave di dump intatti, app invariate in config.yaml.
func TestConfigSetDatabasesOffNonCancellaNiente(t *testing.T) {
	e := newDBEnv(t)
	appendConfig(t, e.cfg, "apps_enabled: true\napps_domain: apps.example.lan\n")
	if code := e.run("config", "set", "databases", "on", "--databases-port-range", "20000-20010"); code != ExitOK {
		t.Fatalf("accensione: exit %d: %s", code, e.errb)
	}
	e.r.calls = nil
	before := string(e.cl.secrets[0].Raw) + string(e.cl.secrets[1].Raw)
	if code := e.run("config", "set", "databases", "off"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, e.errb)
	}
	if len(e.r.calls) != 1 {
		t.Fatalf("attesa la sola chiamata a helm, trovate: %+v", e.r.calls)
	}
	h := helmLines(e.r)[0]
	if !strings.Contains(h, "--set databases.enabled=false") || strings.Count(h, "--set ") != 1 {
		t.Errorf("helm deve cambiare solo databases.enabled: %s", h)
	}
	if len(e.cl.secrets) != 2 || string(e.cl.secrets[0].Raw)+string(e.cl.secrets[1].Raw) != before {
		t.Errorf("i Secret della CA e della chiave di dump non si toccano: %d", len(e.cl.secrets))
	}
	c := e.config(t)
	if c.DatabasesEnabled || !c.AppsEnabled || c.AppsDomain != "apps.example.lan" {
		t.Errorf("config dopo lo spegnimento: databases=%v apps=%v dominio=%q", c.DatabasesEnabled, c.AppsEnabled, c.AppsDomain)
	}
	if c.DatabasesPortFrom != 20000 {
		t.Errorf("l'intervallo scelto non si perde spegnendo: %+v", c)
	}
	if !strings.Contains(e.installConf(t), "DATABASES_ENABLED=false") {
		t.Errorf("install.conf: %s", e.installConf(t))
	}
	if !strings.Contains(e.out.String(), "Nessun database e nessun dato è stato cancellato") {
		t.Errorf("manca l'avviso che niente si cancella: %s", e.out)
	}
	// Riaccendere riusa i Secret (nessun duplicato) e l'intervallo salvato.
	e.out.Reset()
	if code := e.run("config", "set", "databases", "on"); code != ExitOK {
		t.Fatalf("riaccensione: exit %d: %s", code, e.errb)
	}
	if len(e.cl.secrets) != 2 {
		t.Errorf("la riaccensione non deve ricreare i Secret: %d", len(e.cl.secrets))
	}
	hs := helmLines(e.r)
	if last := hs[len(hs)-1]; !strings.Contains(last, "databases.lan.portRange.from=20000") {
		t.Errorf("la riaccensione deve riusare l'intervallo salvato: %s", last)
	}
}

func TestConfigSetDatabasesGiaSpentoNonFaNiente(t *testing.T) {
	e := newDBEnv(t)
	if code := e.run("config", "set", "databases", "off"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, e.errb)
	}
	if len(e.r.calls) != 0 || !strings.Contains(e.out.String(), "già spenti") {
		t.Errorf("calls=%v out=%s", e.r.calls, e.out)
	}
}

// Il preflight di risorse ferma l'accensione prima di qualunque modifica.
func TestConfigSetDatabasesPreflightRisorse(t *testing.T) {
	e := newDBEnv(t)
	e.a.NodeProbe = func() sysinfo.NodeProbe {
		return sysinfo.NodeProbe{MemoryBytes: 8 * gbytes, DiskPath: "/", DiskFreeBytes: 20 * gbytes}
	}
	if code := e.run("config", "set", "databases", "on"); code != ExitRefused {
		t.Fatalf("exit %d (atteso ExitRefused): %s", code, e.errb)
	}
	if len(e.r.calls) != 0 || len(e.cl.secrets) != 0 || e.config(t).DatabasesEnabled {
		t.Errorf("modifiche nonostante il rifiuto: calls=%v secrets=%d", e.r.calls, len(e.cl.secrets))
	}
	for _, want := range []string{"RAM insufficiente per i database gestiti", "disco libero insufficiente per i database gestiti", "Niente è stato modificato"} {
		if !strings.Contains(e.errb.String(), want) {
			t.Errorf("manca %q in: %s", want, e.errb)
		}
	}
	e.errb.Reset()
	if code := e.run("config", "set", "databases", "on", "--skip-preflight"); code != ExitOK {
		t.Fatalf("con --skip-preflight: exit %d: %s", code, e.errb)
	}
}

func TestConfigSetDatabasesUso(t *testing.T) {
	e := newDBEnv(t)
	for _, args := range [][]string{
		{"config", "set", "databases"},
		{"config", "set", "databases", "forse"},
		{"config", "set", "databases", "on", "--databases-port-range", "20010-20000"},
		{"config", "set", "databases", "on", "--databases-port-range", "80-90"},
		{"config", "set", "databases", "on", "--databases-port-range", "abc"},
		{"config", "set", "databases", "off", "--databases-port-range", "20000-20010"},
	} {
		e.errb.Reset()
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: exit %d (atteso ExitUsage): %s", args, code, e.errb)
		}
	}
	if len(e.r.calls) != 0 || len(e.cl.secrets) != 0 {
		t.Errorf("un errore d'uso non deve toccare niente: %v", e.r.calls)
	}
}

// Un chart precedente ai database gestiti non si usa con --reuse-values: si aggiorna prima.
func TestConfigSetDatabasesChartVecchio(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{}
	a, _, errb := hostApp(r, tlsDir)
	a.NodeProbe = bigHost
	if code := a.Run(context.Background(), []string{"config", "set", "databases", "on", "--config", cfg}); code != ExitConfig {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb.String(), "gitstack upgrade --enable-databases") || len(r.calls) != 0 {
		t.Errorf("err=%s calls=%v", errb, r.calls)
	}
}

// gitstack upgrade --enable-databases: valori del chart insieme all'aggiornamento, config.yaml e install.conf.
func TestUpgradeEnableDatabases(t *testing.T) {
	e := newDomEnv(t, cliOld, nil)
	e.a.NodeProbe = bigHost
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.cfg), "chart", "values.yaml"), []byte("databases:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code := e.a.Run(context.Background(), []string{"upgrade", "--config", e.cfg, "--enable-databases", "--databases-port-range", "20000-20010", "--allow-unsigned-mirrors"})
	if code != ExitOK {
		t.Fatalf("exit %d\nout: %s\nerr: %s", code, e.out, e.errb)
	}
	var up string
	for _, h := range e.r.helmCalls {
		if strings.HasPrefix(h, "upgrade ") {
			up = h
		}
	}
	for _, want := range []string{"databases.enabled=true", "databases.lan.portRange.from=20000", "databases.lan.portRange.to=20010"} {
		if !strings.Contains(up, want) {
			t.Errorf("helm upgrade senza %q: %s", want, up)
		}
	}
	c := e.config(t)
	if !c.DatabasesEnabled || c.DatabasesPortFrom != 20000 || c.AppsEnabled {
		t.Errorf("config: databases=%v da=%d apps=%v", c.DatabasesEnabled, c.DatabasesPortFrom, c.AppsEnabled)
	}
	ic, _ := os.ReadFile(filepath.Join(filepath.Dir(e.cfg), "tls", "install.conf"))
	if !strings.Contains(string(ic), "DATABASES_ENABLED=true") {
		t.Errorf("install.conf: %s", ic)
	}
	if !strings.Contains(e.out.String(), "Database gestiti accesi") {
		t.Errorf("output: %s", e.out)
	}
}

func TestUpgradeEnableDatabasesPreflightERifiutoNonToccaNiente(t *testing.T) {
	e := newDomEnv(t, cliOld, nil)
	e.a.NodeProbe = func() sysinfo.NodeProbe { return sysinfo.NodeProbe{MemoryBytes: 8 * gbytes} }
	code := e.a.Run(context.Background(), []string{"upgrade", "--config", e.cfg, "--enable-databases", "--allow-unsigned-mirrors"})
	if code != ExitRefused {
		t.Fatalf("exit %d (atteso ExitRefused)\nerr: %s", code, e.errb)
	}
	if len(e.r.helmCalls) != 0 || e.config(t).DatabasesEnabled {
		t.Errorf("modifiche nonostante il rifiuto: helm=%v", e.r.helmCalls)
	}
}

func TestUpgradeDatabasesUso(t *testing.T) {
	e := newDomEnv(t, cliOld, nil)
	for _, args := range [][]string{
		{"upgrade", "--config", e.cfg, "--databases-port-range", "20000-20010"},
		{"upgrade", "--config", e.cfg, "--skip-preflight"},
		{"upgrade", "--config", e.cfg, "--enable-databases", "--databases-port-range", "10-20"},
	} {
		if code := e.a.Run(context.Background(), args); code != ExitUsage {
			t.Errorf("%v: exit %d (atteso ExitUsage)", args, code)
		}
	}
}

func TestPortRangeConfig(t *testing.T) {
	if f, to, err := config.ParseDatabasesPortRange("15432-15531"); err != nil || f != 15432 || to != 15531 {
		t.Errorf("15432-15531: %d %d %v", f, to, err)
	}
	for _, bad := range []string{"", "15432", "1023-2000", "2000-70000", "3000-2000", "a-b", "1-"} {
		if _, _, err := config.ParseDatabasesPortRange(bad); err == nil {
			t.Errorf("%q: atteso un errore", bad)
		}
	}
}
