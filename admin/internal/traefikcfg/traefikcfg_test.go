package traefikcfg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"gopkg.in/yaml.v3"
)

func repoChart(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "deploy", "gitstack"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, MetricsValuesRel)); err != nil {
		t.Fatalf("chart del repo: %v", err)
	}
	return dir
}

func metrics(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoChart(t), filepath.FromSlash(MetricsValuesRel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRenderValidoConMetricheACMEeGateway(t *testing.T) {
	out, err := Render(Params{MetricsValues: metrics(t), CIGatewayPort: 8443,
		ACME: &ACME{Resolver: "letsencrypt", Email: "a@example.com", CAServer: "https://pebble:14000/dir"}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Kind     string         `yaml:"kind"`
		Metadata map[string]any `yaml:"metadata"`
		Spec     struct {
			ValuesContent string `yaml:"valuesContent"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
	if doc.Kind != "HelmChartConfig" || doc.Metadata["name"] != "traefik" || doc.Metadata["namespace"] != "kube-system" {
		t.Fatalf("intestazione sbagliata: %+v", doc)
	}
	v, err := values(out)
	if err != nil {
		t.Fatal(err)
	}
	prom := v["metrics"].(map[string]any)["prometheus"].(map[string]any)
	if prom["entryPoint"] != "metrics" {
		t.Fatalf("metriche: %v", prom)
	}
	if got := gatewayPortOf(v); got != 8443 {
		t.Fatalf("porta cigateway = %d", got)
	}
	a := acmeOf(v)
	if a == nil || a.Resolver != "letsencrypt" || a.Email != "a@example.com" || a.CAServer != "https://pebble:14000/dir" {
		t.Fatalf("acme = %+v", a)
	}
}

// Stesso testo di write_traefik_manifest in deploy/install.sh (dove c'è bash).
func TestRenderUgualeAlInstaller(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash non disponibile: l'equivalenza con install.sh si prova in CI Linux")
	}
	chart := repoChart(t)
	install := filepath.Join(chart, "..", "install.sh")
	cases := []struct {
		name string
		acme string
		gw   int
		p    Params
		// plan != nil: il manifest Go è quello di Plan (file assente), con ca vuoto nell'installer.
		plan *config.Config
		ca   string
	}{
		{"solo metriche", "no", 0, Params{}, nil, ""},
		{"acme", "yes", 0, Params{ACME: &ACME{Resolver: "letsencrypt", Email: "admin@example.com", CAServer: "https://acme.test/dir"}}, nil, ""},
		{"acme e gateway", "yes", 8443, Params{CIGatewayPort: 8443, ACME: &ACME{Resolver: "letsencrypt", Email: "admin@example.com", CAServer: "https://acme.test/dir"}}, nil, ""},
		{"gateway", "no", 9443, Params{CIGatewayPort: 9443}, nil, ""},
		{"plan: app accese con email, tls internal", "yes", 0, Params{}, &config.Config{TLS: "internal", AppsEnabled: true}, "-"},
		{"plan: app accese con email, tls custom", "yes", 0, Params{}, &config.Config{TLS: "custom", AppsEnabled: true}, "-"},
		{"plan: app spente con email", "no", 0, Params{}, &config.Config{TLS: "internal"}, "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			script := `set -e
export GITSTACK_INSTALL_LIB=1
. "$INSTALL"
set +e
K3S_MANIFESTS_DIR="$OUT"; TRAEFIK_MANIFEST="$OUT/gitstack-traefik.yaml"; OLD_ACME_MANIFEST="$OUT/old.yaml"
GITSTACK_TLS_EMAIL="admin@example.com"; GITSTACK_ACME_CA_SERVER="$CA"
ACME_RESOLVER_NAME=letsencrypt
CI_GATEWAY_ENABLED=` + map[bool]string{true: "1", false: "0"}[c.gw > 0] + `; CI_GATEWAY_PORT=` + itoa(c.gw) + `
write_traefik_manifest "$CHART" ` + c.acme + ` >/dev/null 2>&1
`
			ca := c.ca
			switch ca {
			case "":
				ca = "https://acme.test/dir"
			case "-":
				ca = ""
			}
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = append(os.Environ(), "INSTALL="+install, "OUT="+dir, "CHART="+chart, "CA="+ca)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("install.sh non caricabile: %v\n%s", err, out)
			}
			want, err := os.ReadFile(filepath.Join(dir, "gitstack-traefik.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			c.p.MetricsValues = metrics(t)
			var got []byte
			if c.plan != nil {
				in := inputs(t, c.plan)
				if err := os.MkdirAll(in.TLSDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(in.TLSDir, "install.conf"), []byte("EMAIL=admin@example.com\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				got, _, err = Plan(in)
			} else {
				got, err = Render(c.p)
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.ReplaceAll(string(got), "\r", "") != strings.ReplaceAll(string(want), "\r", "") {
				t.Fatalf("diverso dall'installer\n--- Go\n%s\n--- install.sh\n%s", got, want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "8443"
	}
	return strconv.Itoa(n)
}

func inputs(t *testing.T, cfg *config.Config) *Inputs {
	t.Helper()
	dir := t.TempDir()
	return &Inputs{Cfg: cfg, ChartDir: repoChart(t), TLSDir: filepath.Join(dir, "tls"), Manifest: filepath.Join(dir, "manifests", "gitstack-traefik.yaml")}
}

func TestPlanFileAssente(t *testing.T) {
	in := inputs(t, &config.Config{TLS: "internal"})
	data, st, err := Plan(in)
	if err != nil || st != Missing {
		t.Fatalf("st=%v err=%v", st, err)
	}
	if strings.Contains(string(data), "certificatesResolvers") || strings.Contains(string(data), "cigateway") {
		t.Fatalf("senza letsencrypt né gateway:\n%s", data)
	}
	if err := Write(in.Path(), data); err != nil {
		t.Fatal(err)
	}
	if _, st, _ = Plan(in); st != Aligned {
		t.Fatalf("dopo la scrittura st=%v", st)
	}
}

func TestPlanLetsEncryptDaConfigEInstallConf(t *testing.T) {
	in := inputs(t, &config.Config{TLS: "letsencrypt", CIGatewayEnabled: true, CIGatewayPort: 8443})
	if err := os.MkdirAll(in.TLSDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.TLSDir, "install.conf"), []byte("MODE=letsencrypt\nEMAIL=ops@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _, err := Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := values(data)
	if a := acmeOf(v); a == nil || a.Email != "ops@example.com" {
		t.Fatalf("acme = %+v", a)
	}
	if gatewayPortOf(v) != 8443 {
		t.Fatal("porta cigateway mancante")
	}
}

func TestPlanConservaAcmeEGatewayDelFileEsistente(t *testing.T) {
	in := inputs(t, &config.Config{TLS: "internal"})
	old, _ := Render(Params{MetricsValues: []byte("ports:\n  other:\n    port: 1\n"), CIGatewayPort: 9443,
		ACME: &ACME{Resolver: "le", Email: "x@example.com", CAServer: "https://pebble/dir"}})
	if err := os.MkdirAll(filepath.Dir(in.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.Path(), old, 0o644); err != nil {
		t.Fatal(err)
	}
	data, st, err := Plan(in)
	if err != nil || st != Stale {
		t.Fatalf("st=%v err=%v", st, err)
	}
	v, _ := values(data)
	a := acmeOf(v)
	if a == nil || a.Resolver != "le" || a.Email != "x@example.com" || a.CAServer != "https://pebble/dir" {
		t.Fatalf("acme perso: %+v", a)
	}
	if gatewayPortOf(v) != 9443 {
		t.Fatalf("porta cigateway persa: %d", gatewayPortOf(v))
	}
	if _, ok := v["metrics"]; !ok {
		t.Fatal("metriche non aggiunte")
	}
}

func TestPlanFileAllineatoNonSiRiscrive(t *testing.T) {
	in := inputs(t, &config.Config{TLS: "internal"})
	data, _, _ := Plan(in)
	extra := append(append([]byte{}, data...), []byte("    extraKey: keep\n")...)
	if err := os.MkdirAll(filepath.Dir(in.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.Path(), extra, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, st, err := Plan(in); err != nil || st != Aligned {
		t.Fatalf("st=%v err=%v", st, err)
	}
}

func TestWriteTogliVecchioFileLetsEncrypt(t *testing.T) {
	in := inputs(t, &config.Config{TLS: "internal"})
	dir := filepath.Dir(in.Path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, LegacyACMEName)
	if err := os.WriteFile(old, []byte("vecchio"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _, _ := Plan(in)
	if err := Write(in.Path(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("il vecchio file doveva sparire: %v", err)
	}
}
