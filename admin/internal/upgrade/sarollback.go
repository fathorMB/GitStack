package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
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

// deploymentSA è lo stato letto da `kubectl get deployments -o json`.
type deploymentSA struct {
	Name               string
	ServiceAccountName string
	ServiceAccount     string
}

type deploymentList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					ServiceAccountName string `json:"serviceAccountName"`
					ServiceAccount     string `json:"serviceAccount"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	} `json:"items"`
}

func (o *Options) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	env := []string{"KUBECONFIG=" + o.Cfg.Kubeconfig}
	a := append([]string{"kubectl", "-n", o.Cfg.Namespace}, args...)
	return o.Runner.Run(ctx, env, "k3s", a...)
}

// liveDeployments rilegge i Deployment dal cluster.
func (o *Options) liveDeployments(ctx context.Context) ([]deploymentSA, error) {
	out, err := o.kubectl(ctx, "get", "deployments", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get deployments: %w: %s", err, tail(string(out)))
	}
	var l deploymentList
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("elenco dei Deployment illeggibile: %w", err)
	}
	var res []deploymentSA
	for _, it := range l.Items {
		t := it.Spec.Template.Spec
		res = append(res, deploymentSA{Name: it.Metadata.Name, ServiceAccountName: t.ServiceAccountName, ServiceAccount: t.ServiceAccount})
	}
	return res, nil
}

// staleServiceAccounts rilegge ogni Deployment e dice quali sono legati a un
// ServiceAccount che non esiste. Ogni Deployment controllato è nel log.
func (o *Options) staleServiceAccounts(ctx context.Context, phase string) ([]string, error) {
	live, err := o.liveDeployments(ctx)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, d := range live {
		sa := d.ServiceAccountName
		if sa == "" {
			sa = d.ServiceAccount
		}
		if sa == "" || sa == "default" {
			o.logf("    %s: Deployment %s senza ServiceAccount dedicato (serviceAccountName=%q serviceAccount=%q)", phase, d.Name, d.ServiceAccountName, d.ServiceAccount)
			continue
		}
		out, err := o.kubectl(ctx, "get", "serviceaccount", sa, "-o", "name")
		switch {
		case err == nil:
			o.logf("    %s: Deployment %s usa il ServiceAccount %s, che esiste", phase, d.Name, sa)
		case strings.Contains(string(out), "NotFound"):
			o.logf("    %s: Deployment %s usa il ServiceAccount %s, che NON esiste", phase, d.Name, sa)
			stale = append(stale, d.Name+" → "+sa)
		default:
			return nil, fmt.Errorf("kubectl get serviceaccount %s: %w: %s", sa, err, tail(string(out)))
		}
	}
	if len(live) == 0 {
		o.logf("    %s: nessun Deployment trovato in %s", phase, o.Cfg.Namespace)
	}
	return stale, nil
}

// clearStaleServiceAccounts è il passo dopo helm rollback: azzera i
// ServiceAccount dei Deployment che la revisione di destinazione non lega a
// nessuno, poi RILEGGE i Deployment e fallisce se uno punta ancora a un
// ServiceAccount che non esiste. Ogni passo è nel log.
func (o *Options) clearStaleServiceAccounts(ctx context.Context, rev int) error {
	out, err := o.helmRun(ctx, "get", "manifest", o.Cfg.Release, "--revision", strconv.Itoa(rev), "-n", o.Cfg.Namespace)
	if err != nil {
		return fmt.Errorf("ServiceAccount dopo il rollback non verificati: helm get manifest: %w: %s", err, tail(string(out)))
	}
	names, err := deploymentsWithoutServiceAccount(out)
	if err != nil {
		return fmt.Errorf("ServiceAccount dopo il rollback non verificati: manifest illeggibile: %w", err)
	}
	o.logf("==> ServiceAccount dopo il rollback: manifest della revisione %d letto (%d byte), Deployment senza ServiceAccount: %s", rev, len(out), listOrNone(names))
	const patch = `{"spec":{"template":{"spec":{"serviceAccount":null,"serviceAccountName":null}}}}`
	applyPatch := func(n string) {
		o2, e := o.kubectl(ctx, "patch", "deployment", n, "--type=merge", "-p", patch)
		switch {
		case e == nil:
			o.logf("    patch su %s: %s", n, oneLine(strings.TrimSpace(string(o2))))
		case strings.Contains(string(o2), "NotFound"):
			o.logf("    patch su %s: Deployment non trovato, salto", n)
		default:
			o.logf("    patch su %s non riuscita: %v: %s", n, e, tail(string(o2)))
		}
	}
	for _, n := range names {
		applyPatch(n)
	}
	stale, err := o.staleServiceAccounts(ctx, "dopo la patch")
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		// Ripopolati dal defaulting o da altro: una seconda patch, poi si rilegge.
		o.logf("    ServiceAccount rimasti dopo la patch: %s; riprovo", strings.Join(stale, ", "))
		for _, n := range names {
			applyPatch(n)
		}
		stale, err = o.staleServiceAccounts(ctx, "dopo la seconda patch")
		if err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("dopo il rollback restano Deployment legati a un ServiceAccount che non esiste: %s", strings.Join(stale, ", "))
	}
	o.logf("    nessun Deployment legato a un ServiceAccount inesistente")
	return nil
}

func listOrNone(l []string) string {
	if len(l) == 0 {
		return "nessuno"
	}
	return strings.Join(l, ", ")
}
