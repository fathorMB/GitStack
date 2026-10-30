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
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-442 (H16): da un pod di app una GET /v1 al gateway con GITSTACK_API_URL risponde; una porta
// «di core» sullo stesso pod, e il gateway da un'app senza la regola, restano chiusi. Come
// TestApps_BarriereInUscitaSuK3d gira solo in CI (serve un cluster k3d). Il «gateway» è un pod con due
// listener: 18090 (porta interna, risponde 200) e 18091 (porta di core). Ogni negativo ha il suo
// controllo opposto (stesso bersaglio da un namespace senza policy).
func TestApps_GatewayInternoSuK3d(t *testing.T) {
	e := newDeployEnvOpt(t, false)
	ctx := context.Background()
	gwNS := "gitstack-h16-gw-" + strings.ToLower(uuid.NewString()[:8])
	gw := &apps.GatewayAccess{Namespace: gwNS, Service: "gitstack-gateway", Port: 18090,
		PodLabels: map[string]string{"app.kubernetes.io/name": "gateway", "app.kubernetes.io/instance": "gitstack"}}

	withGW := store.App{ID: uuid.New(), RepoID: uuid.New()}
	restricted := store.App{ID: uuid.New(), RepoID: uuid.New()}
	without := store.App{ID: uuid.New(), RepoID: uuid.New()}
	none := []netip.AddrPort{}
	for a, o := range map[*store.App]apps.NetworkPolicyOptions{
		&withGW:     {Gateway: gw},
		&restricted: {Gateway: gw, Restriction: &none},
		&without:    {},
	} {
		ns := apps.BuildNamespace(*a, false)
		if _, err := e.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		cleanupNamespaceWithDiag(t, e.kube, ns.Name)
		policy := apps.BuildNetworkPolicy(*a, o)
		if _, err := e.kube.NetworkingV1().NetworkPolicies(policy.Namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	systemNS := "gitstack-h16-ctl-" + strings.ToLower(uuid.NewString()[:8])
	for _, name := range []string{gwNS, systemNS} {
		if _, err := e.kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		n := name
		cleanupNamespaceWithDiag(t, e.kube, n)
	}
	serve := `serve() { while true; do printf 'HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok' | nc -l -p $1; done; }; serve 18090 & serve 18091 & wait`
	if _, err := e.kube.CoreV1().Pods(gwNS).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gateway", Namespace: gwNS, Labels: gw.PodLabels},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", serve}}}}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.CoreV1().Services(gwNS).Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: gw.Service, Namespace: gwNS},
		Spec: corev1.ServiceSpec{Selector: gw.PodLabels, Ports: []corev1.ServicePort{{Name: "internal", Port: 18090, TargetPort: intstr.FromInt(18090)}}}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	gwIP := waitPodIP(t, e.kube, gwNS, "gateway")

	root := int64(0)
	probe := func(ns, name, url string, allowed bool) {
		t.Helper()
		labels := map[string]string{}
		if ns != systemNS {
			labels[apps.LabelManaged] = "true"
		}
		ok, ko := "echo OK", "echo KO; exit 1"
		if !allowed {
			ok, ko = ko, ok
		}
		wgetProbe := fmt.Sprintf("if [ \"$(wget -q -O- -T 5 %s 2>/dev/null)\" = ok ]; then %s; else %s; fi", url, ok, ko)
		script := wgetProbe
		if ns != systemNS && allowed {
			script = retryProbeScript(fmt.Sprintf("[ \"$(wget -q -O- -T 5 %s 2>/dev/null)\" = ok ]", url), ok)
		}
		spec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", script},
			SecurityContext: &corev1.SecurityContext{RunAsUser: &root}}}}
		if ns != systemNS {
			spec.InitContainers = []corev1.Container{apps.GateContainer("busybox:1.36", []string{"sh", "-c",
				"w=0; while nc -z -w 1 \"$KUBERNETES_SERVICE_HOST\" \"$KUBERNETES_SERVICE_PORT\"; do w=$((w + 1)); if [ $w -ge 60 ]; then echo 'KO: NetworkPolicy non applicata'; exit 1; fi; sleep 1; done"})}
		}
		if _, err := e.kube.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}, Spec: spec}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := waitPodLog(t, e.kube, ns, name); got != "OK" {
			t.Fatalf("%s: %s", name, got)
		}
		t.Logf("OK: %s", name)
	}
	apiURL := gw.APIURL() // GITSTACK_API_URL come lo vede l'app (nome del Service, via DNS)
	coreLike := fmt.Sprintf("http://%s:18091/v1", gwIP)
	// Controlli opposti: dal namespace senza policy rispondono entrambe.
	probe(systemNS, "ctrl-interna", apiURL, true)
	probe(systemNS, "ctrl-core", coreLike, true)
	// Con la regola: GET /v1 sulla porta interna risponde, l'altra porta no, anche per l'app ristretta.
	probe(apps.Namespace(withGW.ID), "app-api", apiURL, true)
	probe(apps.Namespace(withGW.ID), "app-core-chiusa", coreLike, false)
	probe(apps.Namespace(restricted.ID), "ristretta-api", apiURL, true)
	probe(apps.Namespace(restricted.ID), "ristretta-core-chiusa", coreLike, false)
	// Senza la regola (gateway non configurato) l'app non lo raggiunge.
	probe(apps.Namespace(without.ID), "senza-regola", apiURL, false)
}
