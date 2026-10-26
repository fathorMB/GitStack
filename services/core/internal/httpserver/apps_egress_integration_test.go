//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-380 (H16): le barriere in uscita della NetworkPolicy sono applicate dal CNI di k3d.
// Internet è aperto; la rete privata (qui l'IP del nodo k3d, 172.x), i servizi del cluster e l'API
// di Kubernetes restano chiusi, anche per un'app che gira come root (baseline, H9). La policy è
// costruita senza NodeAddresses, come in produzione senza GITSTACK_EGRESS_NODE_ADDRESSES: il
// blocco dell'IP del nodo viene dalle reti private di pkg/egress, non dall'eccezione del nodo (che
// coprono i test unitari). Ogni negativo ha il suo controllo opposto (stesso bersaglio raggiunto da
// un namespace senza policy). I negativi sono `if nc -z ...; then echo KO; exit 1; fi`, una riga
// ok/ko per controllo.
func TestApps_BarriereInUscitaSuK3d(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	repo := uuid.New()
	source := store.App{ID: uuid.New(), RepoID: repo}
	other := store.App{ID: uuid.New(), RepoID: uuid.New()}

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
	if nodeIP == "" {
		t.Fatal("il nodo non ha InternalIP")
	}
	kubeSvc, err := e.kube.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range []store.App{source, other} {
		ns := apps.BuildNamespace(a, false)
		if _, err := e.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{}) })
	}
	policy := apps.BuildNetworkPolicy(source, apps.NetworkPolicyOptions{})
	if _, err := e.kube.NetworkingV1().NetworkPolicies(policy.Namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Un servizio in ascolto sull'IP del nodo (hostNetwork), fuori da ogni namespace app.
	systemNS := "gitstack-h16-system-" + strings.ToLower(uuid.NewString()[:8])
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
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", "nc -lk -p 18080"}}}}})
	waitPodIP(t, e.kube, systemNS, "node-listener")
	create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: apps.Namespace(other.ID), Labels: map[string]string{apps.LabelManaged: "true"}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", "nc -lk -p 8080"}}}}})
	otherIP := waitPodIP(t, e.kube, apps.Namespace(other.ID), "other")

	// App con root nel container: baseline lo accetta, le barriere valgono lo stesso.
	root := int64(0)
	probe := func(ns, name, host string, port int, allowed bool) {
		t.Helper()
		labels := map[string]string{}
		if ns != systemNS {
			labels[apps.LabelManaged] = "true"
		}
		ok, ko := "echo OK", "echo KO; exit 1"
		if !allowed {
			ok, ko = ko, ok
		}
		script := fmt.Sprintf("if nc -z -w 5 %s %d; then %s; else %s; fi", host, port, ok, ko)
		create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
				Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", script},
				SecurityContext: &corev1.SecurityContext{RunAsUser: &root}}}}})
		if got := waitPodLog(t, e.kube, ns, name); got != "OK" {
			t.Fatalf("%s: %s", name, got)
		}
		t.Logf("OK: %s", name)
	}
	appNS, sysNS := apps.Namespace(source.ID), systemNS
	// Controlli opposti, da un namespace senza NetworkPolicy: gli stessi bersagli SONO raggiungibili.
	// Senza questi, un listener che non parte o una porta che non risponde farebbero passare i negativi.
	probe(sysNS, "ctrl-ip-nodo", nodeIP, 18080, true)
	probe(sysNS, "ctrl-api-nodo", nodeIP, 6443, true)
	probe(sysNS, "ctrl-api-servizio", kubeSvc.Spec.ClusterIP, 443, true)
	probe(sysNS, "ctrl-pod-altro-repo", otherIP, 8080, true)
	// Dal namespace dell'app (root): internet sì, il resto no.
	probe(appNS, "internet-aperto", "1.1.1.1", 443, true)
	probe(appNS, "ip-nodo-privato-lan", nodeIP, 18080, false)
	probe(appNS, "api-kubernetes-nodo", nodeIP, 6443, false)
	probe(appNS, "api-kubernetes-servizio", kubeSvc.Spec.ClusterIP, 443, false)
	probe(appNS, "pod-altro-repo", otherIP, 8080, false)
}
