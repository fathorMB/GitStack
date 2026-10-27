//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-385 (H16): destinazioni private aperte dall'amministratore e restrizione per app, applicate dal
// CNI di k3d. Come TestApps_BarriereInUscitaSuK3d gira solo in CI (serve un cluster k3d). La rete
// privata è qui l'IP del nodo k3d (172.x) con due porte in ascolto: 18080 viene aperta, 18081 no.
// Ogni negativo ha il suo controllo opposto (stesso bersaglio da un namespace senza policy).
// GIT-443: il bersaglio è httpd e non `nc -lk`, che esce dopo la prima connessione (anche un solo
// `nc -z`) e fa sembrare «rifiutata» dalla policy una porta che non ascolta più.
func TestApps_DestinazionePrivataApertaSuK3d(t *testing.T) {
	e := newDeployEnvOpt(t, false)
	ctx := context.Background()
	open := store.App{ID: uuid.New(), RepoID: uuid.New()}       // policy con l'apertura dell'amministratore
	restricted := store.App{ID: uuid.New(), RepoID: uuid.New()} // restretta: solo 18080 e nient'altro
	closed := store.App{ID: uuid.New(), RepoID: uuid.New()}     // senza aperture

	nodes, err := e.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		t.Fatalf("nodi: %v", err)
	}
	var nodeIP string
	for _, a := range nodes.Items[0].Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			nodeIP = a.Address
		}
	}
	addr, err := netip.ParseAddr(nodeIP)
	if err != nil {
		t.Fatalf("InternalIP del nodo %q: %v", nodeIP, err)
	}
	opening := []netip.AddrPort{netip.AddrPortFrom(addr, 18080)}
	only := []netip.AddrPort{netip.AddrPortFrom(addr, 18080)}
	for a, o := range map[*store.App]apps.NetworkPolicyOptions{
		&open:       {PrivateOpenings: opening},
		&restricted: {PrivateOpenings: opening, Restriction: &only},
		&closed:     {},
	} {
		ns := apps.BuildNamespace(*a, false)
		if _, err := e.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{}) })
		policy := apps.BuildNetworkPolicy(*a, o)
		if _, err := e.kube.NetworkingV1().NetworkPolicies(policy.Namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	systemNS := "gitstack-h16-open-" + strings.ToLower(uuid.NewString()[:8])
	if _, err := e.kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: systemNS}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = e.kube.CoreV1().Namespaces().Delete(context.Background(), systemNS, metav1.DeleteOptions{})
	})
	create := func(p *corev1.Pod) {
		if _, err := e.kube.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "node-listener", Namespace: systemNS},
		Spec: corev1.PodSpec{HostNetwork: true, RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", "httpd -f -p 18080 -h /tmp & httpd -f -p 18081 -h /tmp & wait"}}}}})
	waitPodIP(t, e.kube, systemNS, "node-listener")

	root := int64(0)
	probe := func(ns, name string, port int, allowed bool) {
		t.Helper()
		labels := map[string]string{}
		if ns != systemNS {
			labels[apps.LabelManaged] = "true"
		}
		ok, ko := "echo OK", "echo KO; exit 1"
		if !allowed {
			ok, ko = ko, ok
		}
		script := fmt.Sprintf("if nc -z -w 5 %s %d; then %s; else %s; fi", nodeIP, port, ok, ko)
		spec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", script},
			SecurityContext: &corev1.SecurityContext{RunAsUser: &root}}}}
		if ns != systemNS {
			// Come per i pod veri (GIT-418): il probe parte dopo che la policy vale, controllando che l'API
			// di Kubernetes non risponda più.
			spec.InitContainers = []corev1.Container{apps.GateContainer("busybox:1.36", []string{"sh", "-c",
				"w=0; while nc -z -w 1 \"$KUBERNETES_SERVICE_HOST\" \"$KUBERNETES_SERVICE_PORT\"; do w=$((w + 1)); if [ $w -ge 60 ]; then echo 'KO: NetworkPolicy non applicata'; exit 1; fi; sleep 1; done"})}
		}
		create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}, Spec: spec})
		if got := waitPodLog(t, e.kube, ns, name); got != "OK" {
			t.Fatalf("%s: %s", name, got)
		}
		t.Logf("OK: %s", name)
	}
	// Controlli opposti: le due porte rispondono da un namespace senza policy.
	probe(systemNS, "ctrl-18080", 18080, true)
	probe(systemNS, "ctrl-18081", 18081, true)
	// Aperta dall'amministratore: l'app raggiunge quell'indirizzo su quella porta e non l'altra.
	probe(apps.Namespace(open.ID), "aperta-18080", 18080, true)
	probe(apps.Namespace(open.ID), "aperta-18081", 18081, false)
	// Con la restrizione: stessa cosa, e internet non c'è più.
	probe(apps.Namespace(restricted.ID), "ristretta-18080", 18080, true)
	probe(apps.Namespace(restricted.ID), "ristretta-18081", 18081, false)
	// Nessuna apertura: nessuna delle due.
	probe(apps.Namespace(closed.ID), "chiusa-18080", 18080, false)
	probe(apps.Namespace(closed.ID), "chiusa-18081", 18081, false)
}
