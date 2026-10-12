package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
	"github.com/fathorMB/GitStack/admin/internal/sysinfo"
)

// publishTimeout limita la raccolta e la pubblicazione: è un'informazione in
// più, non deve far attendere backup e upgrade.
const publishTimeout = 90 * time.Second

// systemInfo compone il documento di Admin · System dalle fonti dell'host.
func (a *App) systemInfo(ctx context.Context, cfg *config.Config, cfgPath string) sysinfo.Info {
	dir := a.configDir(cfgPath)
	col := &status.Collector{Runner: a.Runner, HTTP: a.HTTP, LookPath: a.LookPath}
	rep := col.Collect(ctx, cfg, dir)
	a.k3sAvailable(ctx, rep)
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	in := sysinfo.Input{
		Cfg:        cfg,
		Report:     rep,
		Backup:     backupstate.Read(dir),
		HelmValues: a.helmValues(ctx, cfg),
		Now:        now,
	}
	if a.NodeProbe != nil {
		in.Node = a.NodeProbe()
	}
	if a.CertExpiry != nil && cfg.TLS != "" && cfg.TLS != "insecure" {
		if at, err := a.CertExpiry(ctx, cfg.Host); err == nil {
			in.CertExpiry = &at
		}
	}
	return sysinfo.Build(in)
}

// helmValues legge i values della release; nil se non si riesce (la rete
// risulta «non configurato»: meglio di un dato inventato).
func (a *App) helmValues(ctx context.Context, cfg *config.Config) map[string]any {
	out, err := a.Runner.Run(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, a.helmBin(),
		"get", "values", cfg.Release, "-n", cfg.Namespace, "-o", "json")
	if err != nil {
		return nil
	}
	var v map[string]any
	if json.Unmarshal(out, &v) != nil {
		return nil
	}
	return v
}

func (a *App) applier(cfg *config.Config) (sysinfo.Applier, error) {
	if a.ApplyManifest != nil {
		return func(ctx context.Context, m []byte) error { return a.ApplyManifest(ctx, m) }, nil
	}
	look := a.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if p, err := look("kubectl"); err == nil {
		return sysinfo.KubectlApplier(p, nil, cfg.Kubeconfig, nil), nil
	}
	if p, err := look("k3s"); err == nil {
		return sysinfo.KubectlApplier(p, []string{"kubectl"}, cfg.Kubeconfig, nil), nil
	}
	return nil, errors.New("né kubectl né k3s nel PATH")
}

// publishSystemInfo scrive il ConfigMap system-info. Non fallisce il comando
// chiamante: chi lo usa stampa l'avviso.
func (a *App) publishSystemInfo(ctx context.Context, cfg *config.Config, cfgPath string) error {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	apply, err := a.applier(cfg)
	if err != nil {
		return err
	}
	return sysinfo.Publish(ctx, cfg, a.systemInfo(ctx, cfg, cfgPath), apply)
}

// autoPublish è la chiamata di backup e upgrade: solo se abilitata (NewApp).
func (a *App) autoPublish(ctx context.Context, cfg *config.Config, cfgPath string) {
	if !a.AutoPublish {
		return
	}
	if err := a.publishSystemInfo(ctx, cfg, cfgPath); err != nil {
		_, _ = fmt.Fprintf(a.Stderr, "AVVISO: Admin · System non aggiornato: %v\n", err)
	}
}

func runSystemInfo(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("system-info", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			a.errorf("system-info: %v", err)
			a.usage(a.Stderr)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 || (pos[0] != "publish" && pos[0] != "show") {
		a.errorf("system-info: uso: gitstack system-info publish|show [--config PERCORSO]")
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	if pos[0] == "show" {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(a.systemInfo(ctx, cfg, path)); err != nil {
			a.errorf("system-info: %v", err)
			return ExitUnexpected
		}
		return ExitOK
	}
	if err := a.publishSystemInfo(ctx, cfg, path); err != nil {
		a.errorf("system-info: %v", err)
		return ExitCluster
	}
	_, _ = fmt.Fprintf(a.Stdout, "Pubblicato: ConfigMap %s/%s (chiave %s)\n", cfg.Namespace, sysinfo.ConfigMapName(cfg.Release), sysinfo.Key)
	return ExitOK
}
