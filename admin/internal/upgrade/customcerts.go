package upgrade

import (
	"context"
	"strings"
)

// Certificati del cliente nel namespace dedicato (GIT-494, regressione di GIT-460).
//
// Fino a GIT-494 core scriveva i Secret gitstack-domain-<id> e il TLSStore `default` nel namespace
// del release (CA interna) o in kube-system (Let's Encrypt), con permessi su TUTTI i Secret di quel
// namespace. Ora vivono in <release>-certs (creato dal chart) e core non ha più nessun verbo altrove.
//
// Migrazione all'upgrade, in due tempi:
//  1. core ricrea da solo i Secret nel namespace nuovo al primo giro (ha il certificato e la chiave
//     cifrata nel database), e il TLSStore default lì;
//  2. qui si tolgono le copie vecchie, ma SOLO quelle che esistono già nel namespace nuovo, così un
//     certificato non resta mai senza Secret. Ciò che core non ha ancora ricreato resta dov'è e lo
//     dice il riepilogo: rilanciare `gitstack upgrade` (idempotente) o cancellare a mano.
//
// Il TLSStore `default` di kube-system si toglie solo se lo ha creato core (nessun
// defaultCertificate, tutti i certificati sono gitstack-domain-*), altrimenti Traefik ne vedrebbe
// due. Quello del release lo toglie helm, perché il chart non lo rende più lì.

const (
	customCertLabel  = "gitstack.io/custom-domain-cert=true"
	customCertPrefix = "gitstack-domain-"
	traefikNS        = "kube-system"
)

func (o *Options) certsNamespace() string { return o.Cfg.Release + "-certs" }

// migrateCustomCerts toglie i Secret e il TLSStore dei certificati del cliente rimasti nei namespace
// vecchi. Non è mai fatale e non fa niente se non c'è niente da togliere.
func (o *Options) migrateCustomCerts(ctx context.Context, res *Result) {
	env := []string{"KUBECONFIG=" + o.Cfg.Kubeconfig}
	newNS := o.certsNamespace()
	kc := func(args ...string) (string, error) {
		out, err := o.Runner.Run(ctx, env, "k3s", append([]string{"kubectl"}, args...)...)
		return string(out), err
	}
	// Senza il namespace nuovo (apps spente, o chart vecchio) non si tocca niente.
	if out, err := kc("get", "namespace", newNS, "-o", "name"); err != nil || !strings.Contains(out, newNS) {
		return
	}
	var kept []string
	moved := 0
	for _, old := range []string{o.Cfg.Namespace, traefikNS} {
		if old == newNS {
			continue
		}
		out, err := kc("-n", old, "get", "secret", "-l", customCertLabel, "-o", "name")
		if err != nil {
			continue
		}
		for _, line := range strings.Fields(out) {
			name := strings.TrimPrefix(line, "secret/")
			if !strings.HasPrefix(name, customCertPrefix) {
				continue
			}
			if got, err := kc("-n", newNS, "get", "secret", name, "-o", "name"); err != nil || !strings.Contains(got, name) {
				kept = append(kept, old+"/"+name)
				continue
			}
			if _, err := kc("-n", old, "delete", "secret", name, "--ignore-not-found"); err != nil {
				kept = append(kept, old+"/"+name)
				continue
			}
			moved++
		}
	}
	if o.removeOldTLSStore(kc, newNS) {
		res.add("TLSStore default di %s (creato da core per i certificati del cliente) rimosso: ora è in %s (GIT-494).", traefikNS, newNS)
	}
	if moved > 0 {
		o.logf("==> Certificati del cliente: %d Secret vecchi tolti, core li ha già ricreati in %s", moved, newNS)
		res.add("Certificati del cliente spostati nel namespace %s (GIT-494): %d Secret vecchi cancellati dopo che core li ha ricreati.", newNS, moved)
	}
	if len(kept) > 0 {
		res.add("Certificati del cliente (GIT-494): core non ha ancora ricreato in %s i Secret %s. Rilancia `gitstack upgrade` fra qualche minuto, o cancellali a mano con `k3s kubectl -n <namespace> delete secret <nome>` quando compaiono nel namespace nuovo.", newNS, strings.Join(kept, ", "))
	}
}

// removeOldTLSStore toglie il TLSStore default di kube-system creato da core, se quello nuovo c'è già.
func (o *Options) removeOldTLSStore(kc func(...string) (string, error), newNS string) bool {
	if newNS == traefikNS {
		return false
	}
	if out, err := kc("-n", newNS, "get", "tlsstores.traefik.io", "default", "-o", "name"); err != nil || !strings.Contains(out, "default") {
		return false
	}
	def, err := kc("-n", traefikNS, "get", "tlsstores.traefik.io", "default", "-o", "jsonpath={.spec.defaultCertificate.secretName}")
	if err != nil || strings.TrimSpace(def) != "" {
		return false
	}
	refs, err := kc("-n", traefikNS, "get", "tlsstores.traefik.io", "default", "-o", "jsonpath={.spec.certificates[*].secretName}")
	if err != nil || strings.TrimSpace(refs) == "" {
		return false
	}
	for _, n := range strings.Fields(refs) {
		if !strings.HasPrefix(n, customCertPrefix) {
			return false
		}
	}
	_, err = kc("-n", traefikNS, "delete", "tlsstores.traefik.io", "default", "--ignore-not-found")
	return err == nil
}
