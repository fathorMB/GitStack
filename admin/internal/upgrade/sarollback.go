package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServiceAccount dopo helm rollback (GIT-479).
//
// L'upgrade con ci.enabled=true (o apps.enabled) lega il Deployment di core a
// un ServiceAccount (templates/core/deployment.yaml, ci-dispatcher). La
// revisione di prima non ha il campo. helm rollback lo toglie con una patch
// su spec.template.spec.serviceAccountName, ma l'API server tiene anche il
// campo deprecato spec.template.spec.serviceAccount, che resta al valore
// nuovo e al defaulting riempie di nuovo serviceAccountName. Intanto il
// rollback ha cancellato il ServiceAccount (non è nella revisione di prima):
// il ReplicaSet di core nasce con un account che non esiste, non crea nessun
// pod («error looking up service account ... not found»), core non torna
// pronto, il gateway (pronto solo se core risponde) nemmeno, e il restore
// esce con «gateway non è tornato pronto» (exit 8 invece di 7).
//
// Dopo helm rollback si rilegge il manifest della revisione di destinazione:
// per ogni Deployment che lì non ha un ServiceAccount si azzerano entrambi i
// campi con una patch merge (con i campi assenti la patch non cambia niente,
// quindi non fa ripartire i pod).

type manifestDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				ServiceAccountName string `yaml:"serviceAccountName"`
				ServiceAccount     string `yaml:"serviceAccount"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

// deploymentsWithoutServiceAccount elenca i Deployment del manifest che non
// dichiarano nessun ServiceAccount.
func deploymentsWithoutServiceAccount(manifest []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(manifest))
	var names []string
	for {
		var d manifestDoc
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		if d.Kind != "Deployment" || d.Metadata.Name == "" {
			continue
		}
		t := d.Spec.Template.Spec
		if t.ServiceAccountName == "" && t.ServiceAccount == "" {
			names = append(names, d.Metadata.Name)
		}
	}
}

// clearStaleServiceAccounts è il passo dopo helm rollback. Non è mai fatale
// da solo: se non riesce lo dice, e l'eventuale guasto lo segnala il restore.
func (o *Options) clearStaleServiceAccounts(ctx context.Context, rev int) {
	out, err := o.helmRun(ctx, "get", "manifest", o.Cfg.Release, "--revision", strconv.Itoa(rev), "-n", o.Cfg.Namespace)
	if err != nil {
		o.logf("    ServiceAccount dopo il rollback non verificati: helm get manifest: %v: %s", err, tail(string(out)))
		return
	}
	names, err := deploymentsWithoutServiceAccount(out)
	if err != nil {
		o.logf("    ServiceAccount dopo il rollback non verificati: manifest illeggibile: %v", err)
		return
	}
	env := []string{"KUBECONFIG=" + o.Cfg.Kubeconfig}
	const patch = `{"spec":{"template":{"spec":{"serviceAccount":null,"serviceAccountName":null}}}}`
	for _, n := range names {
		if o2, e := o.Runner.Run(ctx, env, "k3s", "kubectl", "-n", o.Cfg.Namespace, "patch", "deployment", n, "--type=merge", "-p", patch); e != nil {
			if strings.Contains(string(o2), "NotFound") {
				continue
			}
			o.logf("    %s", fmt.Sprintf("ServiceAccount di %s non azzerato: %v: %s", n, e, tail(string(o2))))
		}
	}
}
