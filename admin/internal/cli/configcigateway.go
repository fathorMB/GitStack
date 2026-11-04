package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"gopkg.in/yaml.v3"
)

const traefikManifest = "/var/lib/rancher/k3s/server/manifests/gitstack-traefik.yaml"

// gatewayManifest conserva le altre impostazioni della HelmChartConfig, incluse
// metriche e ACME, e sostituisce soltanto la porta dedicata.
func gatewayManifest(data []byte, on bool, port int) ([]byte, error) {
	var doc struct {
		APIVersion string         `yaml:"apiVersion"`
		Kind       string         `yaml:"kind"`
		Metadata   map[string]any `yaml:"metadata"`
		Spec       struct {
			ValuesContent string `yaml:"valuesContent"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != "HelmChartConfig" || doc.Metadata["name"] != "traefik" || doc.Metadata["namespace"] != "kube-system" {
		return nil, fmt.Errorf("il manifest non e' la HelmChartConfig traefik attesa")
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(doc.Spec.ValuesContent), &values); err != nil {
		return nil, err
	}
	ports, ok := values["ports"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ports mancanti nel manifest Traefik")
	}
	if on {
		ports["cigateway"] = map[string]any{"port": port, "exposedPort": port, "expose": map[string]any{"default": true}, "protocol": "TCP"}
	} else {
		delete(ports, "cigateway")
	}
	b, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	doc.Spec.ValuesContent = string(b)
	return yaml.Marshal(&doc)
}

func writeGatewayManifest(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gitstack-traefik-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o644); err == nil {
		_, err = f.Write(data)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func runConfigSetCIGateway(ctx context.Context, a *App, args []string) int {
	const cmd = "config set ci-gateway"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	port := fs.Int("port", 0, "porta pubblica")
	var pos []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			a.errorf("%s: %v", cmd, err)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") || *port < 0 || *port > 65535 || (pos[0] == "off" && *port != 0) {
		a.errorf("%s: usa on [--port 1..65535] oppure off", cmd)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	on := pos[0] == "on"
	if *port == 0 {
		*port = cfg.CIGatewayPort
	}
	if on && (cfg.TLS == "" || cfg.TLS == "insecure") {
		a.errorf("%s: serve HTTPS; configura prima TLS per l'host %s", cmd, cfg.Host)
		return ExitConfig
	}
	if on && (!cfg.CIGatewayEnabled || *port != cfg.CIGatewayPort) {
		ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(*port)))
		if err != nil {
			a.errorf("%s: porta %d non disponibile: %v", cmd, *port, err)
			return ExitConfig
		}
		_ = ln.Close()
	}
	if !dirHasChart(cfg.ChartDir) {
		a.errorf("%s: chart non disponibile: aggiorna GitStack", cmd)
		return ExitConfig
	}
	chartValues, err := os.ReadFile(filepath.Join(cfg.ChartDir, "values.yaml"))
	if err != nil || !strings.Contains(string(chartValues), "publicPort:") {
		a.errorf("%s: aggiorna prima il chart di GitStack", cmd)
		return ExitConfig
	}
	if on {
		out, e := a.Runner.Run(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, a.helmBin(), "get", "values", cfg.Release, "-n", cfg.Namespace, "--all", "-o", "json")
		var v struct {
			CI struct {
				Enabled bool `json:"enabled"`
			} `json:"ci"`
		}
		if e != nil || json.Unmarshal(out, &v) != nil || !v.CI.Enabled {
			a.errorf("%s: la CI deve essere attiva; abilitala prima con install.sh --ci", cmd)
			return ExitConfig
		}
	}
	manifest := traefikManifest
	if p := a.Getenv("GITSTACK_TRAEFIK_MANIFEST"); p != "" {
		manifest = p
	}
	old, err := os.ReadFile(manifest)
	if err != nil {
		a.errorf("%s: manifest Traefik: %v", cmd, err)
		return ExitConfig
	}
	next, err := gatewayManifest(old, on, *port)
	if err != nil {
		a.errorf("%s: %v", cmd, err)
		return ExitConfig
	}
	if err = writeGatewayManifest(manifest, next); err != nil {
		a.errorf("%s: manifest Traefik: %v", cmd, err)
		return ExitUnexpected
	}
	sets := []string{"ci.externalGateway.enabled=" + strconv.FormatBool(on)}
	if on {
		sets = append(sets, "ci.externalGateway.host="+cfg.Host, "ci.externalGateway.entryPoint=cigateway", "ci.externalGateway.publicPort="+strconv.Itoa(*port))
	}
	argsHelm := []string{"upgrade", cfg.Release, cfg.ChartDir, "--namespace", cfg.Namespace, "--reuse-values"}
	for _, s := range sets {
		argsHelm = append(argsHelm, "--set", s)
	}
	argsHelm = append(argsHelm, "--wait", "--timeout", "5m")
	if out, e := a.Runner.Run(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, a.helmBin(), argsHelm...); e != nil {
		if rollbackErr := writeGatewayManifest(manifest, old); rollbackErr != nil {
			a.errorf("%s: ripristino manifest fallito: %v", cmd, rollbackErr)
		}
		a.errorf("%s: helm upgrade: %v\n%s", cmd, e, strings.TrimSpace(string(out)))
		return ExitCluster
	}
	if err = config.SetFields(path, map[string]string{"ci_gateway_enabled": strconv.FormatBool(on), "ci_gateway_port": strconv.Itoa(*port)}); err != nil {
		a.errorf("%s: servizi aggiornati, salvataggio configurazione fallito: %v; rilancia il comando", cmd, err)
		return ExitUnexpected
	}
	updateInstallConfKey(a.tlsDirFor(path), "CI_GATEWAY_ENABLED", strconv.FormatBool(on))
	updateInstallConfKey(a.tlsDirFor(path), "CI_GATEWAY_PORT", strconv.Itoa(*port))
	if on {
		_, _ = fmt.Fprintf(a.Stdout, "Gateway acceso: https://%s:%d. Apri la porta TCP; il certificato TLS deve coprire %s e i runner devono fidarsi della sua CA.\n", cfg.Host, *port, cfg.Host)
	} else {
		_, _ = fmt.Fprintln(a.Stdout, "Gateway esterno spento.")
	}
	return ExitOK
}
