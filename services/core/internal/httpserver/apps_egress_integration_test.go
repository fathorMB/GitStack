//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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
	e := newDeployEnvOpt(t, false)
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
		cleanupNamespaceWithDiag(t, e.kube, ns.Name)
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
	cleanupNamespaceWithDiag(t, e.kube, systemNS)
	create := func(p *corev1.Pod) {
		if _, err := e.kube.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "node-listener", Namespace: systemNS},
		Spec: corev1.PodSpec{HostNetwork: true, RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", "httpd -f -p 18080 -h /tmp"}}}}})
	waitPodIP(t, e.kube, systemNS, "node-listener")
	create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: apps.Namespace(other.ID), Labels: map[string]string{apps.LabelManaged: "true"}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{Name: "server", Image: "busybox:1.36", Command: []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}}}}})
	otherIP := waitPodIP(t, e.kube, apps.Namespace(other.ID), "other")

	// App con root nel container: baseline lo accetta, le barriere valgono lo stesso.
	root := int64(0)
	if os.Getenv(envTestCoreImage) == "" {
		t.Log("cancello in shell, immagine di core assente")
	}
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
		spec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", script},
			SecurityContext: &corev1.SecurityContext{RunAsUser: &root}}}}
		if ns != systemNS {
			// GIT-418: come i pod delle app veri, il probe parte dopo il cancello. kube-router applica la
			// NetworkPolicy dopo l'avvio del pod: nei primi secondi l'API (6443) e il kubelet (10250) del nodo
			// erano raggiungibili anche da qui. Il cancello di core (immagine di core) qui ha lo stesso
			// comportamento in shell: attende che l'API Kubernetes non risponda più, ed esce con errore
			// (fail-closed) se non succede. Non si guarda nessuno dei bersagli dei negativi.
			gate := apps.GateContainer("busybox:1.36", []string{"sh", "-c",
				"w=0; while nc -z -w 1 \"$KUBERNETES_SERVICE_HOST\" \"$KUBERNETES_SERVICE_PORT\"; do w=$((w + 1)); if [ $w -ge 60 ]; then echo 'KO: NetworkPolicy non applicata'; exit 1; fi; sleep 1; done"})
			if img := os.Getenv(envTestCoreImage); img != "" {
				// Il cancello vero del prodotto (`core apps-gate`): se l'immagine non c'è nel cluster il probe non parte e il test cade.
				gate = apps.GateContainer(img, apps.GateCommand)
			}
			spec.InitContainers = []corev1.Container{gate}
		}
		create(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}, Spec: spec})
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
	probe(sysNS, "ctrl-kubelet-nodo", nodeIP, 10250, true)
	probe(sysNS, "ctrl-api-servizio", kubeSvc.Spec.ClusterIP, 443, true)
	probe(sysNS, "ctrl-pod-altro-repo", otherIP, 8080, true)
	// Dal namespace dell'app (root): internet sì, il resto no.
	probe(appNS, "internet-aperto", "1.1.1.1", 443, true)
	probe(appNS, "ip-nodo-privato-lan", nodeIP, 18080, false)
	probe(appNS, "api-kubernetes-nodo", nodeIP, 6443, false)
	probe(appNS, "kubelet-nodo", nodeIP, 10250, false)
	probe(appNS, "api-kubernetes-servizio", kubeSvc.Spec.ClusterIP, 443, false)
	probe(appNS, "pod-altro-repo", otherIP, 8080, false)
}

// cleanupNamespaceWithDiag registra la cancellazione del namespace e, se il test è fallito, raccoglie
// prima la diagnostica di rete (GIT-447): NetworkPolicy e pod del namespace, e (una volta per test)
// le regole di kube-router sul nodo, se GITSTACK_TEST_K3D_NODE (nome del container k3d) è impostata.
// Le Cleanup girano in ordine inverso: registrarla subito dopo la creazione del namespace la fa
// girare prima della cancellazione, quando le risorse esistono ancora.
func cleanupNamespaceWithDiag(t *testing.T, kube kubernetes.Interface, ns string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if t.Failed() {
			diagNamespace(t, kube, ns)
		}
		_ = kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	})
}

var diagNodeOnce sync.Map

func diagNamespace(t *testing.T, kube kubernetes.Interface, ns string) {
	ctx := context.Background()
	var b strings.Builder
	fmt.Fprintf(&b, "diagnostica di rete, namespace %s (test fallito)\n", ns)
	if nps, err := kube.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{}); err != nil {
		fmt.Fprintf(&b, "NetworkPolicy: %v\n", err)
	} else {
		for _, np := range nps.Items {
			js, _ := json.MarshalIndent(np.Spec, "", "  ")
			fmt.Fprintf(&b, "NetworkPolicy %s:\n%s\n", np.Name, js)
		}
	}
	if pods, err := kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{}); err != nil {
		fmt.Fprintf(&b, "pod: %v\n", err)
	} else {
		for _, p := range pods.Items {
			fmt.Fprintf(&b, "pod %s fase=%s ip=%s nodo=%s labels=%v\n", p.Name, p.Status.Phase, p.Status.PodIP, p.Spec.NodeName, p.Labels)
		}
	}
	if node := os.Getenv("GITSTACK_TEST_K3D_NODE"); node != "" {
		if _, done := diagNodeOnce.LoadOrStore(t.Name(), true); !done {
			out, err := exec.Command("docker", "exec", node, "sh", "-c",
				`iptables-save | grep -i -E "KUBE-NWPLCY|KUBE-DST|KUBE-SRC|KUBE-POD-FW"; ipset list 2>/dev/null | head -300`).CombinedOutput()
			fmt.Fprintf(&b, "regole kube-router sul nodo (err=%v):\n%s\n", err, out)
		}
	}
	t.Log(b.String())
}

// envTestCoreImage: immagine di core importata nel cluster k3d (k3d-test.sh la importa e la esporta).
const envTestCoreImage = "GITSTACK_TEST_CORE_IMAGE"

// assertGatePassed verifica che ogni pod dell'app nel namespace abbia l'init container «gate»
// terminato con exitCode 0 e che il container dell'app sia pronto (prova anche l'ammissione PSA).
func assertGatePassed(t *testing.T, kube kubernetes.Interface, ns string) {
	t.Helper()
	pods, err := kube.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("pod dell'app %s: %v", ns, err)
	}
	for _, p := range pods.Items {
		var found bool
		for _, s := range p.Status.InitContainerStatuses {
			if s.Name != apps.GateContainerName {
				continue
			}
			found = true
			if s.State.Terminated == nil || s.State.Terminated.ExitCode != 0 {
				t.Fatalf("pod %s: gate non terminato con 0: %+v", p.Name, s.State)
			}
			t.Logf("OK: pod %s: gate exitCode %d (%s)", p.Name, s.State.Terminated.ExitCode, s.State.Terminated.Reason)
		}
		if !found {
			t.Fatalf("pod %s senza init container gate", p.Name)
		}
		ready := false
		for _, c := range p.Status.ContainerStatuses {
			ready = ready || c.Ready
		}
		if !ready && p.DeletionTimestamp == nil {
			t.Fatalf("pod %s: il container dell'app non è pronto", p.Name)
		}
	}
}

// TestApps_CancelloVeroSenzaPolicySuK3d è il controllo opposto del cancello del prodotto
// (`core apps-gate`): in un namespace SENZA NetworkPolicy l'API Kubernetes resta raggiungibile, quindi
// il gate deve terminare con exitCode 1 («ancora raggiungibile») e il container principale non
// deve mai partire. Serve GITSTACK_TEST_CORE_IMAGE.
func TestApps_CancelloVeroSenzaPolicySuK3d(t *testing.T) {
	img := os.Getenv(envTestCoreImage)
	if img == "" {
		t.Skip("GITSTACK_TEST_CORE_IMAGE non impostata: serve l'immagine di core importata nel cluster")
	}
	e := newDeployEnvOpt(t, false)
	ctx := context.Background()
	ns := "gitstack-h16-gate-" + strings.ToLower(uuid.NewString()[:8])
	if _, err := e.kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	gate := apps.GateContainer(img, apps.GateCommand)
	gate.Env = []corev1.EnvVar{{Name: apps.EnvGateTimeout, Value: "5"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gate-aperto", Namespace: ns},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, InitContainers: []corev1.Container{gate},
			Containers: []corev1.Container{{Name: "main", Image: "busybox:1.36", Command: []string{"sh", "-c", "echo MAIN-PARTITO"}}}}}
	if _, err := e.kube.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		p, err := e.kube.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
		if err == nil && len(p.Status.InitContainerStatuses) > 0 && p.Status.InitContainerStatuses[0].State.Terminated != nil {
			term := p.Status.InitContainerStatuses[0].State.Terminated
			if term.ExitCode != 1 {
				t.Fatalf("gate exitCode %d, atteso 1: %+v", term.ExitCode, term)
			}
			log, _ := e.kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: apps.GateContainerName}).DoRaw(ctx)
			if !strings.Contains(string(log), "ancora raggiungibile") {
				t.Fatalf("log del gate senza «ancora raggiungibile»: %q", log)
			}
			for _, c := range p.Status.ContainerStatuses {
				if c.State.Running != nil || c.State.Terminated != nil {
					t.Fatalf("il container principale è partito nonostante il gate fallito: %+v", c.State)
				}
			}
			t.Logf("OK: gate exitCode %d, log: %s", term.ExitCode, strings.TrimSpace(string(log)))
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("il gate non è terminato entro 180 s")
}
