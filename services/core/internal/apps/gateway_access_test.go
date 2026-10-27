package apps

import (
	"net/netip"
	"reflect"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

func testGateway() *GatewayAccess {
	return &GatewayAccess{
		Namespace: "gitstack", Service: "gitstack-gateway", Port: 8081,
		PodLabels: map[string]string{"app.kubernetes.io/name": "gateway", "app.kubernetes.io/instance": "gitstack"},
	}
}

// gatewayRules sono le regole di egress verso il namespace del gateway.
func gatewayRules(p *networkingv1.NetworkPolicy) []networkingv1.NetworkPolicyEgressRule {
	var out []networkingv1.NetworkPolicyEgressRule
	for _, r := range p.Spec.Egress {
		for _, to := range r.To {
			if to.NamespaceSelector != nil && to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == "gitstack" {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// GIT-442 (H16): con le opzioni del gateway c'è una regola verso i soli pod del gateway, sulla sola
// porta interna: anche per un'app con la restrizione dell'uscita.
func TestBuildNetworkPolicy_GatewayInterno(t *testing.T) {
	app, _ := testApp()
	restricted := []netip.AddrPort{ap("93.184.216.34:443")}
	for name, opt := range map[string]NetworkPolicyOptions{
		"aperta":    {Gateway: testGateway()},
		"ristretta": {Gateway: testGateway(), Restriction: &restricted},
	} {
		t.Run(name, func(t *testing.T) {
			rules := gatewayRules(BuildNetworkPolicy(app, opt))
			if len(rules) != 1 || len(rules[0].To) != 1 {
				t.Fatalf("regole verso il gateway: %+v", rules)
			}
			to := rules[0].To[0]
			if to.IPBlock != nil || to.PodSelector == nil || !reflect.DeepEqual(to.PodSelector.MatchLabels, testGateway().PodLabels) {
				t.Fatalf("destinazione non limitata ai pod del gateway: %+v", to)
			}
			if len(rules[0].Ports) != 1 || rules[0].Ports[0].Port.IntValue() != 8081 || *rules[0].Ports[0].Protocol != "TCP" {
				t.Fatalf("porte: %+v", rules[0].Ports)
			}
		})
	}
}

// Senza opzioni (o con un'opzione incompleta) la policy è identica a quella di prima.
func TestBuildNetworkPolicy_SenzaGatewayInvariata(t *testing.T) {
	app, _ := testApp()
	base := BuildNetworkPolicy(app, NetworkPolicyOptions{})
	for name, g := range map[string]*GatewayAccess{"nil": nil, "incompleta": {Namespace: "gitstack", Port: 8081}} {
		got := BuildNetworkPolicy(app, NetworkPolicyOptions{Gateway: g})
		if !reflect.DeepEqual(base, got) {
			t.Errorf("%s: la policy cambia senza gateway configurato", name)
		}
	}
	if len(gatewayRules(base)) != 0 {
		t.Fatal("regola verso il gateway senza opzioni")
	}
}

// GITSTACK_API_URL c'è quando il gateway è configurato (anche per un'app ristretta), manca altrimenti.
func TestBuildDeployment_APIURL(t *testing.T) {
	app, ver := testApp()
	env := func(o BuildOptions) (string, bool) {
		v, ok := envValues(BuildDeployment(app, ver, o).Spec.Template.Spec.Containers[0].Env)[EnvAPIURL]
		return v, ok
	}
	const want = "http://gitstack-gateway.gitstack.svc.cluster.local:8081/v1"
	for name, o := range map[string]BuildOptions{
		"configurato": {Replicas: 1, Gateway: testGateway()},
		"ristretta":   {Replicas: 1, Gateway: testGateway(), EgressRestricted: true},
	} {
		if got, ok := env(o); !ok || got != want {
			t.Errorf("%s: GITSTACK_API_URL = %q (presente %v), voluto %q", name, got, ok, want)
		}
	}
	for name, o := range map[string]BuildOptions{
		"nil":        {Replicas: 1},
		"incompleto": {Replicas: 1, Gateway: &GatewayAccess{Namespace: "gitstack"}},
	} {
		if got, ok := env(o); ok {
			t.Errorf("%s: GITSTACK_API_URL = %q, non deve esserci", name, got)
		}
	}
}
