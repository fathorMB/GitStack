package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// Segreti di servizio per chiamante (GIT-235, F9 del registro ASVS in
// docs/security/asvs-l2.md).
//
// Le installazioni create prima di GIT-235 hanno un solo Secret
// `<release>-identity-service` (chiave `secret`), condiviso da gateway,
// identity, core e git. Il chart nuovo genera il Secret
// `<release>-service-secrets` con una chiave per chiamante (gateway, core,
// git) e non usa più quello vecchio. `gitstack upgrade` fa la migrazione in
// due punti:
//
//  1. prima di helm upgrade toglie dai valori della release
//     identity.serviceSecret.existingSecret, che il chart nuovo rifiuta: i
//     segreti per chiamante li genera il chart (serviceSecrets.existingSecret
//     è il valore che lo sostituisce);
//  2. a upgrade riuscito e servizi sani cancella il Secret vecchio, così la
//     chiave che il servizio git poteva usare per firmare qualunque identità
//     non esiste più nel cluster.
//
// Un rollback rimette il chart vecchio, che rigenera da solo il Secret
// condiviso (lookup assente → nuovo valore casuale).

// legacyServiceSecretValue è il valore tolto dai valori della release.
const legacyServiceSecretValue = "identity.serviceSecret"

// dropLegacyServiceSecretValues toglie identity.serviceSecret dai valori JSON
// della release. ok dice se c'era qualcosa da togliere.
func dropLegacyServiceSecretValues(in []byte) (out []byte, existing string, ok bool, err error) {
	var vals map[string]any
	if err := json.Unmarshal(in, &vals); err != nil {
		return in, "", false, err
	}
	identity, _ := vals["identity"].(map[string]any)
	if identity == nil {
		return in, "", false, nil
	}
	sec, present := identity["serviceSecret"]
	if !present {
		return in, "", false, nil
	}
	if m, _ := sec.(map[string]any); m != nil {
		existing, _ = m["existingSecret"].(string)
	}
	delete(identity, "serviceSecret")
	if len(identity) == 0 {
		delete(vals, "identity")
	}
	out, err = json.Marshal(vals)
	if err != nil {
		return in, "", false, err
	}
	return bytes.TrimSpace(out), existing, true, nil
}

// legacyServiceSecretName è il Secret condiviso creato dal chart precedente.
func (o *Options) legacyServiceSecretName() string {
	return o.Cfg.Release + "-identity-service"
}

// removeLegacyServiceSecret cancella il Secret condiviso di prima di F9.
// Non è mai fatale: l'aggiornamento è riuscito e i servizi non lo usano più.
// userSecret è il Secret dato dall'utente (identity.serviceSecret.existingSecret),
// che non si tocca mai.
func (o *Options) removeLegacyServiceSecret(ctx context.Context, res *Result, userSecret string) {
	name := o.legacyServiceSecretName()
	if userSecret != "" && userSecret == name {
		return
	}
	env := []string{"KUBECONFIG=" + o.Cfg.Kubeconfig}
	// Si cancella solo se il Secret nuovo esiste: un cluster che il chart
	// nuovo non ha ancora toccato usa ancora quello vecchio.
	newName := o.Cfg.Release + "-service-secrets"
	if out, err := o.Runner.Run(ctx, env, "k3s", "kubectl", "-n", o.Cfg.Namespace, "get", "secret", newName, "-o", "name"); err != nil || !strings.Contains(string(out), newName) {
		return
	}
	out, err := o.Runner.Run(ctx, env, "k3s", "kubectl", "-n", o.Cfg.Namespace, "delete", "secret", name, "--ignore-not-found")
	if err != nil {
		o.logf("    Secret %s non cancellato: %v: %s", name, err, tail(string(out)))
		res.add("Segreti di servizio per chiamante attivi (F9). Il Secret condiviso di prima, %s, non è stato cancellato: rimuovilo con `k3s kubectl -n %s delete secret %s`.", name, o.Cfg.Namespace, name)
		return
	}
	if strings.Contains(string(out), "deleted") {
		o.logf("==> Cancellato il Secret condiviso %s: ogni servizio ha ora il suo segreto", name)
		res.add("Segreti di servizio: un segreto per chiamante (gateway, core, git) nel Secret %s-service-secrets; il Secret condiviso %s è stato cancellato (F9).", o.Cfg.Release, name)
	}
}
