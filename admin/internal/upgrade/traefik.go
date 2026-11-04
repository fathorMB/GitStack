package upgrade

import (
	"path/filepath"

	"github.com/fathorMB/GitStack/admin/internal/traefikcfg"
)

// HelmChartConfig di Traefik (GIT-534).
//
// Il manifest gitstack-traefik.yaml lo scriveva solo deploy/install.sh (GIT-403): un'installazione
// precedente, solo aggiornata da allora, non lo ha mai avuto, e senza di esso mancano le metriche di
// Traefik e `config set ci-gateway on` non ha dove aggiungere la porta. Qui lo si scrive o lo si
// riallinea con la generazione condivisa (traefikcfg), con i valori delle metriche del chart.

func (o *Options) traefikInputs(chartDir string) *traefikcfg.Inputs {
	tlsDir := o.TLSDir
	if tlsDir == "" && o.ConfigFile != "" {
		tlsDir = filepath.Join(filepath.Dir(o.ConfigFile), "tls")
	}
	return &traefikcfg.Inputs{Cfg: o.Cfg, ChartDir: chartDir, TLSDir: tlsDir, Manifest: o.TraefikManifest}
}

// traefikDryRun dice cosa farebbe l'upgrade, col chart appena scaricato.
func (o *Options) traefikDryRun(res *Result, chartDir string) {
	in := o.traefikInputs(chartDir)
	_, st, err := traefikcfg.Plan(in)
	if err != nil {
		res.add("Traefik: non riesco a calcolare il manifest (%v).", err)
		return
	}
	res.add("%s", traefikcfg.Describe(in.Path(), st))
}

// syncTraefikManifest scrive o riallinea il manifest e toglie il vecchio file del solo Let's
// Encrypt. Mai fatale: un errore finisce nel riepilogo.
func (o *Options) syncTraefikManifest(res *Result) {
	chartDir := o.Cfg.ChartDir
	if chartDir == "" {
		chartDir = DefaultChartDir
	}
	in := o.traefikInputs(chartDir)
	data, st, err := traefikcfg.Plan(in)
	if err != nil {
		res.add("Traefik: manifest NON scritto (%v). Rilancia deploy/install.sh o «gitstack config set ci-gateway».", err)
		return
	}
	if st == traefikcfg.Aligned {
		traefikcfg.RemoveLegacy(in.Path())
		return
	}
	if err = traefikcfg.Write(in.Path(), data); err != nil {
		res.add("Traefik: scrittura di %s fallita: %v", in.Path(), err)
		return
	}
	o.logf("==> Traefik: %s (%s)", in.Path(), st)
	if st == traefikcfg.Missing {
		res.add("Traefik: scritta la HelmChartConfig %s (metriche di GIT-403).", in.Path())
	} else {
		res.add("Traefik: riallineata la HelmChartConfig %s.", in.Path())
	}
}

func (o *Options) chartDirOrDefault() string {
	if o.Cfg.ChartDir != "" {
		return o.Cfg.ChartDir
	}
	return DefaultChartDir
}
