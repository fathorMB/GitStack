package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// registryTrust dice a containerd di quale CA fidarsi per il registry di GitStack (GIT-425).
//
// Con TLS internal il registry del repo (https://<host>/<owner>/<repo>) è firmato dalla CA interna,
// che containerd non conosce: senza ca_file le app con un'immagine del repo cadono in
// «x509: certificate signed by unknown authority». La CA è sempre lo stesso file
// (<cartella TLS>/ca.crt): dopo un restore il file sul disco è quello del backup, e containerd lo
// rilegge a ogni pull; il riavvio di k3s serve solo se registries.yaml cambia (host diverso).
type registryTrust struct {
	// Hosts: gli host con cui si raggiunge GitStack (host[:porta] come negli indirizzi delle immagini).
	Hosts []string
	// CAFile: il file PEM della CA interna.
	CAFile string
}

// hosts ritorna gli host ordinati e senza doppioni; vuoto se manca il file della CA.
func (t registryTrust) hosts() []string {
	if t.CAFile == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, h := range t.Hosts {
		if h = strings.TrimSpace(h); h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// registryTrust costruisce la fiducia per la configurazione: solo con TLS internal. Gli host sono
// cfg.Host più quelli di install.conf (HOSTS) e gli eventuali extra.
func (a *App) registryTrust(cfg *config.Config, tlsDir string, extra ...string) registryTrust {
	if cfg.TLS != "internal" {
		return registryTrust{}
	}
	hosts := append([]string{cfg.Host}, extra...)
	if b, err := os.ReadFile(filepath.Join(tlsDir, "install.conf")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "HOSTS="); ok {
				hosts = append(hosts, strings.Split(v, ",")...)
			}
		}
	}
	return registryTrust{Hosts: hosts, CAFile: filepath.Join(tlsDir, "ca.crt")}
}

// syncRegistryTrust riallinea registries.yaml alla CA interna e all'host attuali e riavvia k3s se
// serve: se il file cambia, oppure se la CA ora sul disco è diversa da caBefore (nil = non si sa).
// Lo usano `gitstack restore` e `gitstack config set host`. Senza TLS internal non fa niente.
func (a *App) syncRegistryTrust(ctx context.Context, cfg *config.Config, tlsDir string, caBefore []byte, extraHosts ...string) error {
	trust := a.registryTrust(cfg, tlsDir, extraHosts...)
	if len(trust.hosts()) == 0 {
		return nil
	}
	paths := a.networkPaths()
	changed, err := writeIfChanged(paths.registries(), renderRegistries(cfg.Network, paths.caCopy(), trust), 0o600)
	if err != nil {
		return fmt.Errorf("registries.yaml: %w", err)
	}
	if !changed && caBefore != nil {
		now, _ := os.ReadFile(trust.CAFile)
		changed = !bytes.Equal(now, caBefore)
	}
	if !changed {
		return nil
	}
	_, _ = fmt.Fprintln(a.Stdout, "==> Riavvio k3s (containerd rilegge la CA interna del registry)")
	if out, err := a.Runner.Run(ctx, nil, "systemctl", "restart", "k3s"); err != nil {
		return fmt.Errorf("systemctl restart k3s: %w %s", err, strings.TrimSpace(string(out)))
	}
	return a.waitNodeReady(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig})
}
