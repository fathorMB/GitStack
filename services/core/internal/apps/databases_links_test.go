package apps

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
)

// dbRules sono le regole di uscita che puntano a un database collegato (selettore di namespace e pod).
func dbRules(p *networkingv1.NetworkPolicy, id uuid.UUID) []networkingv1.NetworkPolicyEgressRule {
	var out []networkingv1.NetworkPolicyEgressRule
	for _, r := range p.Spec.Egress {
		for _, to := range r.To {
			if to.NamespaceSelector != nil && to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == databases.Namespace(id) {
				out = append(out, r)
			}
		}
	}
	return out
}

// DB11: l'app collegata esce verso i soli pod del suo database, sulla 5432, per selettore.
func TestBuildNetworkPolicy_UscitaVersoIlDatabaseCollegato(t *testing.T) {
	app, _ := testApp()
	db := uuid.New()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{Databases: []uuid.UUID{db}})
	rules := dbRules(p, db)
	if len(rules) != 1 {
		t.Fatalf("regole verso il database: %+v", p.Spec.Egress)
	}
	r := rules[0]
	if len(r.To) != 1 || r.To[0].IPBlock != nil || r.To[0].PodSelector == nil {
		t.Fatalf("la regola e' per selettore di namespace e pod, mai per IP: %+v", r.To)
	}
	for k, v := range databases.PodLabels(db) {
		if r.To[0].PodSelector.MatchLabels[k] != v {
			t.Errorf("il selettore dei pod non porta %s=%s: %v", k, v, r.To[0].PodSelector.MatchLabels)
		}
	}
	if len(r.Ports) != 1 || r.Ports[0].Port.IntValue() != 5432 || *r.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("solo TCP 5432: %+v", r.Ports)
	}
}

func TestBuildNetworkPolicy_SenzaDatabaseNiente(t *testing.T) {
	app, _ := testApp()
	db := uuid.New()
	if got := dbRules(BuildNetworkPolicy(app, NetworkPolicyOptions{}), db); len(got) != 0 {
		t.Fatalf("un'app non collegata non ha regole verso un database: %+v", got)
	}
}

// H16 + DB11: con l'uscita ristretta (anche vuota, che chiude tutto) la regola del database resta, e non entra
// internet. Un database NON collegato non compare.
func TestBuildNetworkPolicy_UscitaRistrettaRaggiungeIlDatabaseCollegato(t *testing.T) {
	app, _ := testApp()
	linked, other := uuid.New(), uuid.New()
	empty := []netip.AddrPort{}
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{Databases: []uuid.UUID{linked}, Restriction: &empty})
	if len(dbRules(p, linked)) != 1 {
		t.Fatalf("la restrizione deve lasciare la regola del database collegato: %+v", p.Spec.Egress)
	}
	if len(dbRules(p, other)) != 0 {
		t.Fatal("un database non collegato non e' raggiungibile")
	}
	for _, r := range p.Spec.Egress {
		for _, to := range r.To {
			if to.IPBlock != nil && to.IPBlock.CIDR == "0.0.0.0/0" {
				t.Fatalf("internet aperto nonostante la restrizione: %+v", r)
			}
		}
	}
}

// DB10: la CA pubblica dei database collegati e' montata in sola lettura in /etc/gitstack/db (PGSSLROOTCERT).
func TestBuildDeployment_MontaLaCADeiDatabaseSoloSeCollegata(t *testing.T) {
	app, ver := testApp()
	plain := BuildDeployment(app, ver, BuildOptions{Replicas: 1})
	for _, v := range plain.Spec.Template.Spec.Volumes {
		if v.Name == "db-ca" {
			t.Fatal("app non collegata: niente volume della CA")
		}
	}
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 1, DatabaseCA: DatabaseCASecret})
	var vol *corev1.Volume
	for i, v := range d.Spec.Template.Spec.Volumes {
		if v.Name == "db-ca" {
			vol = &d.Spec.Template.Spec.Volumes[i]
		}
	}
	if vol == nil || vol.Secret == nil || vol.Secret.SecretName != DatabaseCASecret {
		t.Fatalf("volume della CA: %+v", d.Spec.Template.Spec.Volumes)
	}
	var m *corev1.VolumeMount
	for i, vm := range d.Spec.Template.Spec.Containers[0].VolumeMounts {
		if vm.Name == "db-ca" {
			m = &d.Spec.Template.Spec.Containers[0].VolumeMounts[i]
		}
	}
	if m == nil || m.MountPath != "/etc/gitstack/db" || !m.ReadOnly {
		t.Fatalf("mount della CA: %+v", d.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
	if databases.CAMountFile != "/etc/gitstack/db/ca.crt" {
		t.Fatalf("PGSSLROOTCERT = %s", databases.CAMountFile)
	}
}
