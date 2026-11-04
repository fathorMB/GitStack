// Package traefikcfg genera la HelmChartConfig `traefik` di kube-system
// (/var/lib/rancher/k3s/server/manifests/gitstack-traefik.yaml): metriche di
// GIT-403, resolver ACME se TLS è letsencrypt, porta `cigateway` se il gateway
// esterno della CI è acceso.
//
// È l'unica generazione in Go, condivisa da `gitstack upgrade` e da
// `gitstack config set ci-gateway` (GIT-534). Il testo è lo stesso di
// write_traefik_manifest in deploy/install.sh: l'equivalenza la prova
// traefikcfg_test.go confrontando con i vettori dell'installer.
package traefikcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"gopkg.in/yaml.v3"
)

// Percorsi e nomi dell'installer.
const (
	// DefaultManifest è il file della HelmChartConfig letto da k3s.
	DefaultManifest = "/var/lib/rancher/k3s/server/manifests/gitstack-traefik.yaml"
	// LegacyACMEName è il vecchio file del solo Let's Encrypt (stesso oggetto traefik), che
	// l'installer toglie.
	LegacyACMEName = "gitstack-traefik-letsencrypt.yaml"
	// MetricsValuesRel è il file dei valori delle metriche, relativo al chart.
	MetricsValuesRel = "files/traefik-metrics-values.yaml"
	// DefaultResolver è il nome del resolver ACME dell'installer (ACME_RESOLVER_NAME).
	DefaultResolver = "letsencrypt"

	cigatewayInternalPort = 18443
	defaultCIGatewayPort  = 8443
)

// ACME sono i parametri del resolver Let's Encrypt.
type ACME struct {
	Resolver string
	Email    string
	CAServer string
}

// Params sono gli ingressi di Render.
type Params struct {
	// MetricsValues è il contenuto di files/traefik-metrics-values.yaml del chart.
	MetricsValues []byte
	// CIGatewayPort > 0 = porta pubblica del gateway esterno della CI (entrypoint cigateway).
	CIGatewayPort int
	// ACME nil = niente resolver.
	ACME *ACME
}

const header = `# Scritto da deploy/install.sh (metriche di Traefik GIT-403; Let's Encrypt N5/GIT-143). Non
# modificarlo a mano: rilancia l'installer.
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
`

// Render produce il manifest, byte per byte come l'installer.
func Render(p Params) ([]byte, error) {
	if len(bytes.TrimSpace(p.MetricsValues)) == 0 {
		return nil, errors.New("valori delle metriche di Traefik vuoti")
	}
	var b strings.Builder
	b.WriteString(header)
	for _, line := range strings.Split(strings.ReplaceAll(string(p.MetricsValues), "\r", ""), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	if p.CIGatewayPort > 0 {
		fmt.Fprintf(&b, "      cigateway:\n        port: %d\n        exposedPort: %d\n        expose:\n          default: true\n        protocol: TCP\n",
			cigatewayInternalPort, p.CIGatewayPort)
	}
	if a := p.ACME; a != nil {
		name := a.Resolver
		if name == "" {
			name = DefaultResolver
		}
		b.WriteString("    persistence:\n      enabled: true\n      path: /data\n      size: 128Mi\n    certificatesResolvers:\n")
		fmt.Fprintf(&b, "      %s:\n        acme:\n", name)
		if a.Email != "" {
			fmt.Fprintf(&b, "          email: %s\n", a.Email)
		}
		if a.CAServer != "" {
			fmt.Fprintf(&b, "          caServer: %s\n", a.CAServer)
		}
		b.WriteString("          storage: /data/acme.json\n          httpChallenge:\n            entryPoint: web\n")
	}
	return []byte(b.String()), nil
}

// Inputs sono ciò da cui si ricava il manifest voluto.
type Inputs struct {
	Cfg *config.Config
	// ChartDir è il chart di cui prendere i valori delle metriche.
	ChartDir string
	// TLSDir contiene install.conf (EMAIL per il resolver ACME).
	TLSDir string
	// Manifest è il file della HelmChartConfig (default DefaultManifest).
	Manifest string
}

func (in *Inputs) manifest() string {
	if in.Manifest != "" {
		return in.Manifest
	}
	return DefaultManifest
}

// State è il confronto fra il file sul disco e il manifest voluto.
type State int

// Valori di State.
const (
	Missing State = iota // il file non c'è
	Stale                // c'è ma manca qualcosa di quanto voluto (es. le metriche)
	Aligned              // c'è e contiene tutto quanto voluto
)

func (s State) String() string {
	switch s {
	case Missing:
		return "assente"
	case Stale:
		return "da riallineare"
	default:
		return "allineato"
	}
}

// Plan calcola il manifest voluto e lo confronta col file. Conserva ACME e porta cigateway del
// file esistente (o del vecchio file del solo Let's Encrypt); senza resolver, lo ricava dalla
// configurazione come install.sh: tls letsencrypt, o app accese con email ACME. Un file che contiene già tutto il voluto è Aligned:
// non va riscritto, così le modifiche in più (es. la CA di Pebble nelle prove) restano.
func Plan(in *Inputs) (data []byte, st State, err error) {
	mv, err := os.ReadFile(filepath.Join(in.ChartDir, filepath.FromSlash(MetricsValuesRel)))
	if err != nil {
		return nil, Missing, fmt.Errorf("valori delle metriche di Traefik nel chart: %w", err)
	}
	path := in.manifest()
	existing, rerr := os.ReadFile(path)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return nil, Missing, rerr
	}
	p := Params{MetricsValues: mv}
	var have map[string]any
	if rerr == nil {
		have, _ = values(existing)
		p.ACME = acmeOf(have)
		p.CIGatewayPort = gatewayPortOf(have)
	}
	if p.ACME == nil {
		if old, e := os.ReadFile(filepath.Join(filepath.Dir(path), LegacyACMEName)); e == nil {
			ov, _ := values(old)
			p.ACME = acmeOf(ov)
		}
	}
	if in.Cfg != nil {
		if in.Cfg.CIGatewayEnabled {
			p.CIGatewayPort = in.Cfg.CIGatewayPort
			if p.CIGatewayPort <= 0 {
				p.CIGatewayPort = defaultCIGatewayPort
			}
		}
		// Stessa regola di ACME_ENABLED in install.sh (GIT-459): letsencrypt, oppure app accese con
		// un'email ACME (EMAIL di install.conf).
		if p.ACME == nil {
			email := readKV(filepath.Join(in.TLSDir, "install.conf"), "EMAIL")
			if in.Cfg.TLS == "letsencrypt" || (in.Cfg.AppsEnabled && email != "") {
				p.ACME = &ACME{Resolver: DefaultResolver, Email: email}
			}
		}
	}
	data, err = Render(p)
	if err != nil {
		return nil, Missing, err
	}
	if rerr != nil {
		return data, Missing, nil
	}
	want, err := values(data)
	if err != nil {
		return nil, Missing, err
	}
	if have != nil && contains(have, want) {
		return data, Aligned, nil
	}
	return data, Stale, nil
}

// Write scrive il manifest in modo atomico (mode 0644, cartella creata se manca) e toglie il
// vecchio file del solo Let's Encrypt, come l'installer.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // cartella dei manifest di k3s
		return err
	}
	_ = os.Remove(filepath.Join(dir, LegacyACMEName))
	f, err := os.CreateTemp(dir, ".gitstack-traefik-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o644); err == nil { //nolint:gosec // k3s legge i manifest come root, come l'installer
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

// RemoveLegacy toglie il vecchio file del solo Let's Encrypt accanto a path.
func RemoveLegacy(path string) {
	_ = os.Remove(filepath.Join(filepath.Dir(path), LegacyACMEName))
}

// Path è il percorso effettivo del manifest di in.
func (in *Inputs) Path() string { return in.manifest() }

func values(manifest []byte) (map[string]any, error) {
	var doc struct {
		Spec struct {
			ValuesContent string `yaml:"valuesContent"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(manifest, &doc); err != nil {
		return nil, err
	}
	var v map[string]any
	if err := yaml.Unmarshal([]byte(doc.Spec.ValuesContent), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func acmeOf(v map[string]any) *ACME {
	res, _ := v["certificatesResolvers"].(map[string]any)
	for name, r := range res {
		m, _ := r.(map[string]any)
		acme, ok := m["acme"].(map[string]any)
		if !ok {
			continue
		}
		a := &ACME{Resolver: name}
		a.Email, _ = acme["email"].(string)
		a.CAServer, _ = acme["caServer"].(string)
		return a
	}
	return nil
}

func gatewayPortOf(v map[string]any) int {
	ports, _ := v["ports"].(map[string]any)
	gw, _ := ports["cigateway"].(map[string]any)
	switch n := gw["exposedPort"].(type) {
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// contains: ogni chiave di want è in have con lo stesso valore (le mappe si confrontano a fondo).
func contains(have, want map[string]any) bool {
	for k, w := range want {
		h, ok := have[k]
		if !ok {
			return false
		}
		wm, wok := w.(map[string]any)
		hm, hok := h.(map[string]any)
		if wok && hok {
			if !contains(hm, wm) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(h, w) {
			return false
		}
	}
	return true
}

func readKV(file, key string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Describe è la riga del riepilogo/dry-run.
func Describe(path string, st State) string {
	switch st {
	case Missing:
		return fmt.Sprintf("Traefik: scriverei %s (HelmChartConfig con le metriche di GIT-403; file assente).", path)
	case Stale:
		return fmt.Sprintf("Traefik: riallineerei %s (mancano le impostazioni attese, ACME e porta cigateway restano).", path)
	}
	return fmt.Sprintf("Traefik: %s già allineato.", path)
}
