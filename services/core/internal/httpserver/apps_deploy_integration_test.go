//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// GIT-343 (v3-a, H7/H12): deploy a mano, salute, ritorno automatico, riavvio, rollback e scala.

// Permessi e abilitazione: senza controller i deploy rispondono 503, `read` 403 (H5).
func TestApps_DeployPermessiESenzaController(t *testing.T) {
	e := newAppsEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, base + "/web/deploys", `{"image":"sha-1"}`},
		{http.MethodPost, base + "/web/rollback", `{"version":1}`},
		{http.MethodPost, base + "/web/restart", ``},
		{http.MethodPut, base + "/web/scale", `{"copies":2}`},
	} {
		e.want(e.do(c.method, c.path, "carol", c.body), http.StatusForbidden, "forbidden")
		e.want(e.do(c.method, c.path, "bob", c.body), http.StatusServiceUnavailable, "apps_unavailable")
	}
}

// H9: le policy generate dal controller sono applicate davvero dal CNI di k3d,
// non solo confrontate come manifest. I probe usano IP dei pod, mentre il
// proxy prova anche la risoluzione DNS che la policy apre verso kube-dns.
func TestApps_IsolamentoSuK3d(t *testing.T) {
	// Niente controller.Run: Collect tratterebbe come orfani (e cancellerebbe) i
	// namespace creati qui con BuildNamespace per app che non stanno nel DB.
	e := newDeployEnvOpt(t, false)
	ctx := context.Background()
	repo := uuid.New()
	newApp := func() store.App { return store.App{ID: uuid.New(), RepoID: repo} }
	source, peer := newApp(), newApp()
	other := store.App{ID: uuid.New(), RepoID: uuid.New()}
	for _, a := range []store.App{source, peer, other} {
		ns := apps.BuildNamespace(a, false)
		if _, err := e.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.kube.NetworkingV1().NetworkPolicies(ns.Name).Create(ctx, apps.BuildNetworkPolicy(a), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		cleanupNamespaceWithDiag(t, e.kube, ns.Name)
	}
	// I servizi GitStack e il proxy sono esterni agli spazi app e senza label GitStack.
	systemNS := "gitstack-h9-system-" + strings.ToLower(uuid.NewString()[:8])
	if _, err := e.kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: systemNS}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cleanupNamespaceWithDiag(t, e.kube, systemNS)
	server := func(ns, name, image string, labels map[string]string, command []string, ports []corev1.ContainerPort) string {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways, Containers: []corev1.Container{{Name: "server", Image: image, Command: command, Ports: ports}}}}
		p, err := e.kube.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return waitPodIP(t, e.kube, p.Namespace, p.Name)
	}
	appLabels := map[string]string{apps.LabelManaged: "true"}
	// GIT-447: bersagli httpd -f (resta in ascolto per più connessioni), non `nc -lk` che esce dopo la prima.
	peerIP := server(apps.Namespace(peer.ID), "peer", "busybox:1.36", appLabels, []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}, []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}})
	postgresIP := server(systemNS, "postgres", "busybox:1.36", map[string]string{"app.kubernetes.io/name": "postgres"}, []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}, nil)
	natsIP := server(systemNS, "nats", "busybox:1.36", map[string]string{"app.kubernetes.io/name": "nats"}, []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}, nil)
	coreIP := server(systemNS, "core", "busybox:1.36", map[string]string{"app.kubernetes.io/name": "core"}, []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}, nil)
	httpIP := server(systemNS, "http-target", "busybox:1.36", map[string]string{"app.kubernetes.io/name": "http-target"}, []string{"sh", "-c", "mkdir -p /w && echo ok > /w/index.html && exec httpd -f -p 8080 -h /w"}, nil)
	directIP := server(systemNS, "direct-target", "busybox:1.36", map[string]string{"app.kubernetes.io/name": "direct-target"}, []string{"sh", "-c", "httpd -f -p 8081 -h /tmp"}, nil)
	otherIP := server(apps.Namespace(other.ID), "other", "busybox:1.36", appLabels, []string{"sh", "-c", "httpd -f -p 8080 -h /tmp"}, nil)
	probe := func(ns, name, ip string, port int, allowed bool, labels map[string]string) {
		// kube-router applica la NetworkPolicy al pod appena creato in modo
		// asincrono: un nc che passa subito dopo l'avvio non prova nulla. Una
		// probe negativa ritenta finche' il collegamento non e' chiuso per 3
		// tentativi consecutivi; se resta aperto per tutto il tempo (40 tentativi)
		// fallisce. Una probe positiva resta un tentativo solo.
		var check string
		if allowed {
			check = fmt.Sprintf("if nc -z -w 2 %s %d; then echo OK; else echo KO; exit 1; fi", ip, port)
		} else {
			check = fmt.Sprintf(`opened=0; blocked=0; i=0; while [ $i -lt 40 ]; do i=$((i+1)); if nc -z -w 2 %s %d; then opened=$((opened+1)); blocked=0; else blocked=$((blocked+1)); if [ $blocked -ge 3 ]; then echo "OK aperti=$opened"; exit 0; fi; fi; sleep 1; done; echo "KO aperti=$opened"; exit 1`, ip, port)
		}
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", check}}}}}
		if _, err := e.kube.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := waitPodLog(t, e.kube, p.Namespace, p.Name); !strings.HasPrefix(got, "OK") {
			t.Fatalf("%s: %s", name, got)
		} else {
			// Per le negative «aperti=N» con N>0 prova che la policy e' stata applicata in ritardo.
			t.Logf("OK: %s (%s)", name, got)
		}
	}
	probe(apps.Namespace(source.ID), "same-repo", peerIP, 8080, true, appLabels)
	probe(apps.Namespace(source.ID), "postgres", postgresIP, 8080, false, appLabels)
	probe(apps.Namespace(source.ID), "nats", natsIP, 8080, false, appLabels)
	probe(apps.Namespace(source.ID), "core", coreIP, 8080, false, appLabels)
	probe(apps.Namespace(source.ID), "other-repo", otherIP, 8080, false, appLabels)
	probe("kube-system", "traefik-ingress", peerIP, 8080, true, map[string]string{"app.kubernetes.io/name": "traefik"})
	probe("kube-system", "non-traefik-ingress", peerIP, 8080, false, nil)

	proxyIP := server(systemNS, "proxy", "vimagick/tinyproxy@sha256:72b441b95ee1e641af948f68f09492f9f795ead72b73954414e339168c98ad8c", map[string]string{"app.kubernetes.io/name": "tinyproxy"}, nil, nil)
	policy := apps.BuildNetworkPolicy(source, apps.NetworkPolicyOptions{ProxyEnv: map[string]string{"HTTP_PROXY": "http://" + proxyIP + ":8888"}})
	if _, err := e.kube.NetworkingV1().NetworkPolicies(policy.Namespace).Update(ctx, policy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// La policy aggiornata e tinyproxy possono arrivare dopo l'avvio del pod: si ritenta fino a 20 volte (~120 s, sotto i 180 s di waitPodLog); lo stderr dei tentativi intermedi va in /tmp/err, cosi' il log resta «OK».
	proxyCheck := fmt.Sprintf("i=0; while [ $i -lt 20 ]; do i=$((i+1)); if wget -q -T 5 -O /dev/null http://%s:8080 2>/tmp/err; then echo OK; exit 0; fi; sleep 1; done; echo \"KO $(cat /tmp/err)\"; exit 1", httpIP)
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "proxy-egress", Namespace: apps.Namespace(source.ID), Labels: appLabels}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "busybox:1.36", Env: []corev1.EnvVar{{Name: "http_proxy", Value: "http://" + proxyIP + ":8888"}, {Name: "HTTP_PROXY", Value: "http://" + proxyIP + ":8888"}}, Command: []string{"sh", "-c", proxyCheck}}}}}
	if _, err := e.kube.CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := waitPodLog(t, e.kube, p.Namespace, p.Name); got != "OK" {
		t.Fatalf("proxy-egress: %s", got)
	}
	logs, err := e.kube.CoreV1().Pods(systemNS).GetLogs("proxy", &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil || !strings.Contains(string(logs), httpIP) {
		t.Fatalf("proxy senza richiesta verso %s: %v %s", httpIP, err, logs)
	}
	t.Logf("OK: proxy-egress")
	probe(apps.Namespace(source.ID), "direct-external", directIP, 8081, false, appLabels)

	// Pod Security è applicata dall'API server: baseline blocca privilegi e
	// hostPath; restricted blocca anche l'immagine che tenta di girare come root.
	rejected := func(ns, name string, spec corev1.PodSpec) {
		t.Helper()
		_, err := e.kube.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: spec}, metav1.CreateOptions{})
		if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "violates PodSecurity") {
			t.Fatalf("%s: atteso Forbidden violates PodSecurity, ricevuto %v", name, err)
		}
		t.Logf("OK: %s rifiutato da PodSecurity", name)
	}
	privileged := true
	rejected(apps.Namespace(source.ID), "privileged", corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "busybox:1.36", SecurityContext: &corev1.SecurityContext{Privileged: &privileged}}}})
	rejected(apps.Namespace(source.ID), "hostpath", corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "busybox:1.36", VolumeMounts: []corev1.VolumeMount{{Name: "host", MountPath: "/host"}}}}, Volumes: []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}}})
	restricted := store.App{ID: uuid.New(), RepoID: repo}
	rns := apps.BuildNamespace(restricted, true)
	if _, err := e.kube.CoreV1().Namespaces().Create(ctx, rns, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = e.kube.CoreV1().Namespaces().Delete(context.Background(), rns.Name, metav1.DeleteOptions{})
	})
	f := false
	rootSC := &corev1.SecurityContext{AllowPrivilegeEscalation: &f, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}, RunAsUser: ptr(int64(0)), RunAsNonRoot: &f}
	rejected(rns.Name, "root", corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "busybox:1.36", SecurityContext: rootSC}}})
	trueValue := true
	nonRoot := rootSC.DeepCopy()
	nonRoot.RunAsUser = ptr(int64(10001))
	nonRoot.RunAsNonRoot = &trueValue
	for _, tc := range []struct {
		name, ns string
		sc       *corev1.SecurityContext
	}{{"restricted-nonroot", rns.Name, nonRoot}, {"baseline-root", apps.Namespace(source.ID), rootSC}} {
		if _, err := e.kube.CoreV1().Pods(tc.ns).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: tc.ns}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "busybox:1.36", SecurityContext: tc.sc}}}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("%s deve essere accettato: %v", tc.name, err)
		}
		t.Logf("OK: %s accettato", tc.name)
	}
}

func ptr(v int64) *int64 { return &v }

func waitPodIP(t *testing.T, kube kubernetes.Interface, ns, name string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		p, err := kube.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil && p.Status.PodIP != "" && p.Status.Phase == corev1.PodRunning {
			return p.Status.PodIP
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("pod %s/%s senza IP", ns, name)
	return ""
}

func waitPodLog(t *testing.T, kube kubernetes.Interface, ns, name string) string {
	t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		p, err := kube.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil && (p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed) {
			if p.Status.Phase != corev1.PodSucceeded {
				log, _ := kube.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(context.Background())
				t.Fatalf("probe %s/%s fallito (log: %s): %+v", ns, name, strings.TrimSpace(string(log)), p.Status)
			}
			r, err := kube.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{}).Stream(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			return strings.TrimSpace(string(b))
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("probe %s/%s non concluso", ns, name)
	return ""
}

type appsDeployEnv struct {
	*appsEnv
	kube kubernetes.Interface
	dyn  dynamic.Interface
	ns   string
}

func newDeployEnv(t *testing.T) *appsDeployEnv { return newDeployEnvOpt(t, true) }

// newDeployEnvOpt: runController=false non avvia ctl.Run, per i test che creano
// namespace gestiti fuori dal DB.
func newDeployEnvOpt(t *testing.T, runController bool) *appsDeployEnv {
	return newDeployEnvWith(t, runController, deployEnvOptions{})
}

// deployEnvOptions: ritocchi per i test che provano un pezzo del cluster in più (GIT-425).
type deployEnvOptions struct {
	// tune ritocca la Config del controller prima di crearlo.
	tune func(*apps.Config)
	// publicURL è l'URL pubblico di GitStack (e quindi l'host del registry); vuoto: git.example.com.
	publicURL string
	// controllerKubeconfig (GIT-424): se non è vuoto il controller (e solo lui) parla al cluster con
	// quel kubeconfig; le asserzioni e la pulizia dei test restano con l'amministratore.
	controllerKubeconfig string
}

// envTestKubeconfigCore (GIT-424): kubeconfig col token del ServiceAccount di core, con l'RBAC
// reso dal chart (apps-rbac.yaml). Il kubeconfig di k3d è cluster-admin: da solo non prova mai
// i permessi del controller.
const envTestKubeconfigCore = "GITSTACK_TEST_KUBECONFIG_CORE"

// Nomi esportati da k3d-test.sh insieme al kubeconfig: ServiceAccount di core come
// `namespace/nome` e ClusterRole modello delle app.
const (
	envTestCoreSA       = "GITSTACK_TEST_CORE_SA"
	envTestWorkloadRole = "GITSTACK_TEST_WORKLOAD_ROLE"
)

func newDeployEnvWith(t *testing.T, runController bool, opt deployEnvOptions) *appsDeployEnv {
	t.Helper()
	controllerKubeconfig := opt.controllerKubeconfig
	kc := os.Getenv("GITSTACK_TEST_KUBECONFIG")
	if kc == "" {
		t.Skip("GITSTACK_TEST_KUBECONFIG non impostata: serve un cluster k3d (internal/apps/k3d-test.sh)")
	}
	if os.Getenv(envTestCoreImage) == "" {
		// GIT-422: senza immagine del cancello core rifiuta ogni deploy.
		t.Skip("GITSTACK_TEST_CORE_IMAGE non impostata: core rifiuta i deploy senza cancello (GIT-422)")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	// Il chart crea la PriorityClass delle app (ADR 0002); su un k3d vuoto i pod senza non nascono.
	pc := &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: "gitstack-apps"}, Value: 100000}
	if _, err := kube.SchedulingV1().PriorityClasses().Create(context.Background(), pc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	ctlKube, ctlDyn := kubernetes.Interface(kube), dyn
	if controllerKubeconfig != "" {
		crc, err := clientcmd.BuildConfigFromFlags("", controllerKubeconfig)
		if err != nil {
			t.Fatal(err)
		}
		if ctlKube, err = kubernetes.NewForConfig(crc); err != nil {
			t.Fatal(err)
		}
		if ctlDyn, err = dynamic.NewForConfig(crc); err != nil {
			t.Fatal(err)
		}
	}
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base := &issuesEnv{t: t, pool: pool, id: id}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	appsCfg := apps.Config{
		AppsDomain: "apps.example.com", ProgressTimeout: 75 * time.Second, FailureGrace: 6 * time.Second,
		PollInterval: 500 * time.Millisecond, LoopInterval: time.Second, SyncEvery: 2 * time.Second,
		Secrets: keys,
		// GIT-418: con GITSTACK_TEST_CORE_IMAGE i pod delle app hanno il cancello vero (immagine di core importata nel cluster).
		GateImage: os.Getenv(envTestCoreImage),
	}
	if controllerKubeconfig != "" {
		// GIT-424: come in produzione, il controller si lega al ruolo modello nel namespace di ogni
		// app; senza questi due campi ensureRoleBinding non crea il RoleBinding.
		appsCfg.ControllerServiceAccount = os.Getenv(envTestCoreSA)
		appsCfg.WorkloadRole = os.Getenv(envTestWorkloadRole)
		if appsCfg.ControllerServiceAccount == "" || appsCfg.WorkloadRole == "" {
			t.Fatalf("%s e %s vanno impostate insieme a %s", envTestCoreSA, envTestWorkloadRole, envTestKubeconfigCore)
		}
	}
	if opt.tune != nil {
		opt.tune(&appsCfg)
	}
	publicURL := opt.publicURL
	if publicURL == "" {
		publicURL = "https://git.example.com"
	}
	ctl := apps.New(store.New(pool), ctlKube, ctlDyn, appsCfg)
	base.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: publicURL}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now), httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}), httpserver.WithAppController(ctl))
	env := &appsDeployEnv{appsEnv: &appsEnv{issuesEnv: base, clock: clock, git: git}, kube: kube, dyn: dyn}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if runController {
		go ctl.Run(ctx)
	}
	return env
}

func (e *appsDeployEnv) version(path string, n int) openapi.AppVersion {
	e.t.Helper()
	return e.versionOf(path+"/versions/"+itoa(n), "alice")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// waitState aspetta che la versione arrivi allo stato e ne ritorna i dati.
func (e *appsDeployEnv) waitState(path string, n int, want string) openapi.AppVersion {
	e.t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		v := e.version(path, n)
		if string(v.State) == want {
			return v
		}
		if string(v.State) != "in_progress" || time.Now().After(deadline) {
			e.t.Fatalf("versione %d in stato %s (%v), voluto %s", n, v.State, deref(v.StateReason), want)
		}
		time.Sleep(time.Second)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (e *appsDeployEnv) getApp(path string) openapi.App {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, "alice", "")
	e.want(rec, http.StatusOK, "")
	return e.app(rec)
}

func (e *appsDeployEnv) waitApp(path, what string, ok func(openapi.App) bool) openapi.App {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		a := e.getApp(path)
		if ok(a) {
			return a
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s: stato %s, copie pronte %d/%d", what, a.Status, a.ReadyCopies, a.Copies)
		}
		time.Sleep(time.Second)
	}
}

// traffic fa richieste continue al Service dell'app (via API server) e conta quelle riuscite e perse.
type traffic struct {
	ok, lost atomic.Int64
	stop     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	firstErr string
	at       []string
}

func (e *appsDeployEnv) startTraffic() *traffic {
	tr := &traffic{stop: make(chan struct{})}
	for i := 0; i < 3; i++ {
		tr.wg.Add(1)
		go func() {
			defer tr.wg.Done()
			for {
				select {
				case <-tr.stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := e.kube.CoreV1().Services(e.ns).ProxyGet("http", apps.WorkloadName, "80", "/", nil).DoRaw(ctx)
				cancel()
				if err != nil {
					tr.lost.Add(1)
					tr.mu.Lock()
					tr.at = append(tr.at, time.Now().Format("15:04:05.000"))
					if tr.firstErr == "" {
						tr.firstErr = err.Error()
					}
					tr.mu.Unlock()
				} else {
					tr.ok.Add(1)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}
	return tr
}

func (tr *traffic) finish() (ok, lost int64, first string) {
	close(tr.stop)
	tr.wg.Wait()
	return tr.ok.Load(), tr.lost.Load(), tr.firstErr
}

func (e *appsDeployEnv) assertNoLoss(tr *traffic, step string) {
	e.t.Helper()
	ok, lost := tr.ok.Load(), tr.lost.Load()
	if lost != 0 {
		e.t.Fatalf("%s: %d richieste perse su %d (prima: %s) alle %v, ora %s", step, lost, ok+lost, tr.firstErr, tr.at, time.Now().Format("15:04:05.000"))
	}
	if ok == 0 {
		e.t.Fatalf("%s: nessuna richiesta riuscita", step)
	}
	e.t.Logf("%s: %d richieste, 0 perse", step, ok)
}

// Su k3d: la versione parte solo dopo la salute ok; l'avvio che fallisce lascia la precedente;
// riavvio, rollback e scala a 2 copie non perdono richieste.
func TestApps_ControllerSuK3d(t *testing.T) {
	e := newDeployEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	const v1, v2 = "docker.io/traefik/whoami:v1.10.1", "docker.io/traefik/whoami:v1.10.2"

	rec := e.do(http.MethodPost, base, "alice", `{"name":"web","image":"`+v1+`","port":80,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	e.ns = apps.Namespace(uuid.UUID(created.App.Id))
	path := base + "/web"
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), e.ns, metav1.DeleteOptions{}) })

	t.Run("la versione risulta partita solo dopo la salute ok", func(t *testing.T) {
		e.t = t
		var sawInProgress bool
		deadline := time.Now().Add(4 * time.Minute)
		for {
			v := e.version(path, 1)
			d, err := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
			if v.State == "serving" {
				if err != nil || d.Status.ReadyReplicas < 1 || d.Status.AvailableReplicas < 1 {
					t.Fatalf("versione «serving» ma il Deployment non è pronto: %v %+v", err, d.Status)
				}
				break
			}
			if v.State != "in_progress" || time.Now().After(deadline) {
				t.Fatalf("versione 1: %s %s", v.State, deref(v.StateReason))
			}
			sawInProgress = true
			if err == nil && d.Status.AvailableReplicas >= 1 && d.Status.ReadyReplicas >= 1 {
				// Pronto nel cluster ma non ancora scritto: la scrittura segue entro un ciclo.
				time.Sleep(500 * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !sawInProgress {
			t.Log("la versione 1 era già «serving» alla prima lettura")
		}
		a := e.waitApp(path, "app running", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 1 })
		if a.ActiveVersion != 1 {
			t.Fatalf("versione attiva %d", a.ActiveVersion)
		}
		// Service e Deployment sono quelli del modello: immagine per tag, nessun token montato.
		d, _ := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if d.Spec.Template.Spec.Containers[0].Image != v1 || *d.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("Deployment: %+v", d.Spec.Template.Spec)
		}
		if _, err := e.kube.CoreV1().Services(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{}); err != nil {
			t.Fatalf("Service: %v", err)
		}
		if os.Getenv(envTestCoreImage) != "" {
			assertGatePassed(t, e.kube, e.ns)
		} else {
			t.Log("cancello di rete non provato: GITSTACK_TEST_CORE_IMAGE assente")
		}
	})

	tr := e.startTraffic()
	t.Cleanup(func() {
		select {
		case <-tr.stop:
		default:
			tr.finish()
		}
	})

	t.Run("scala a 2 copie", func(t *testing.T) {
		e.t = t
		e.want(e.do(http.MethodPut, path+"/scale", "carol", `{"copies":2}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPut, path+"/scale", "bob", `{"copies":2}`)
		e.want(rec, http.StatusOK, "")
		if a := e.app(rec); a.Copies != 2 {
			t.Fatalf("copie %d", a.Copies)
		}
		e.waitApp(path, "2 copie pronte", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 2 })
		// Oltre la quota dell'owner (4000m di default, 100m a copia): 409 con quanto manca.
		e.want(e.do(http.MethodPut, path+"/scale", "bob", `{"copies":50}`), http.StatusConflict, "insufficient_capacity")
		if a := e.getApp(path); a.Copies != 2 {
			t.Fatalf("copie dopo il rifiuto: %d", a.Copies)
		}
		e.assertNoLoss(tr, "scala")
	})

	podNames := func() map[string]bool {
		l, err := e.kube.CoreV1().Pods(e.ns).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, p := range l.Items {
			out[p.Name] = true
		}
		return out
	}

	t.Run("deploy con due copie: aggiornamento senza richieste perse", func(t *testing.T) {
		e.t = t
		e.want(e.do(http.MethodPost, path+"/deploys", "carol", `{"image":"`+v2+`"}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPost, path+"/deploys", "bob", `{"image":"`+v2+`"}`)
		e.want(rec, http.StatusAccepted, "")
		e.want(e.do(http.MethodPost, path+"/deploys", "bob", `{"image":"`+v1+`"}`), http.StatusConflict, "deploy_in_progress")
		v := e.waitState(path, 2, "serving")
		if v.Trigger != "manual" || v.Image.Reference != v2 {
			t.Fatalf("versione 2: %+v", v)
		}
		a := e.waitApp(path, "v2 a regime", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 2 })
		if a.ActiveVersion != 2 || a.Image.Reference != v2 {
			t.Fatalf("app: versione %d, immagine %s", a.ActiveVersion, a.Image.Reference)
		}
		if p := e.version(path, 1); p.State != "superseded" {
			t.Fatalf("la versione 1 è %s", p.State)
		}
		e.assertNoLoss(tr, "deploy")
	})

	t.Run("riavvio una copia alla volta senza richieste perse", func(t *testing.T) {
		e.t = t
		before := podNames()
		e.want(e.do(http.MethodPost, path+"/restart", "carol", ``), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPost, path+"/restart", "bob", ``), http.StatusAccepted, "")
		deadline := time.Now().Add(3 * time.Minute)
		for {
			old := 0
			for n := range podNames() {
				if before[n] {
					old++
				}
			}
			a := e.getApp(path)
			if old == 0 && a.Status == "running" && a.ReadyCopies == 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("riavvio non concluso: %d pod vecchi, stato %s %d/2", old, a.Status, a.ReadyCopies)
			}
			time.Sleep(time.Second)
		}
		if a := e.getApp(path); a.ActiveVersion != 2 {
			t.Fatalf("il riavvio non crea versioni: attiva %d", a.ActiveVersion)
		}
		e.assertNoLoss(tr, "riavvio")
	})

	t.Run("rollback alla precedente senza richieste perse", func(t *testing.T) {
		e.t = t
		e.want(e.do(http.MethodPost, path+"/rollback", "bob", `{"version":2}`), http.StatusUnprocessableEntity, "")
		e.want(e.do(http.MethodPost, path+"/rollback", "bob", `{"version":9}`), http.StatusUnprocessableEntity, "")
		rec := e.do(http.MethodPost, path+"/rollback", "bob", `{"version":1}`)
		e.want(rec, http.StatusAccepted, "")
		v := e.waitState(path, 3, "serving")
		if v.Trigger != "rollback" || v.RollbackOf == nil || *v.RollbackOf != 1 || v.Image.Reference != v1 {
			t.Fatalf("versione 3: %+v", v)
		}
		a := e.waitApp(path, "rollback a regime", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 2 })
		if a.ActiveVersion != 3 || a.Image.Reference != v1 {
			t.Fatalf("app dopo il rollback: versione %d, immagine %s", a.ActiveVersion, a.Image.Reference)
		}
		e.assertNoLoss(tr, "rollback")
	})

	// Avvio che fallisce: resta in esecuzione la versione 3 e il deploy è «fallito» con il motivo.
	failing := func(t *testing.T, number int, body, wantReason string) {
		t.Helper()
		e.t = t
		e.want(e.do(http.MethodPost, path+"/deploys", "bob", body), http.StatusAccepted, "")
		v := e.waitState(path, number, "failed")
		if !strings.Contains(strings.ToLower(deref(v.StateReason)), strings.ToLower(wantReason)) {
			t.Fatalf("motivo %q, atteso %q", deref(v.StateReason), wantReason)
		}
		a := e.getApp(path)
		if a.ActiveVersion != 3 || a.Image.Reference != v1 || a.RunningPreviousVersion == nil || !*a.RunningPreviousVersion {
			t.Fatalf("deve restare la versione 3: %+v", a)
		}
		if a.LastDeploy == nil || a.LastDeploy.State != "failed" || a.LastDeploy.Version != number || deref(a.LastDeploy.Reason) == "" {
			t.Fatalf("ultimo deploy: %+v", a.LastDeploy)
		}
		a = e.waitApp(path, "versione 3 ancora a regime", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 2 })
		d, err := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if err != nil || d.Spec.Template.Spec.Containers[0].Image != v1 || d.Status.AvailableReplicas != 2 {
			t.Fatalf("Deployment dopo il fallimento: %v %+v", err, d.Status)
		}
		e.assertNoLoss(tr, "deploy fallito")
	}

	t.Run("immagine che non esiste: resta la precedente", func(t *testing.T) {
		failing(t, 4, `{"image":"docker.io/traefik/whoami:non-esiste-343"}`, "pull")
	})
	t.Run("immagine che termina subito: resta la precedente", func(t *testing.T) {
		failing(t, 5, `{"image":"docker.io/library/busybox:1.36"}`, "continuazione")
	})
	t.Run("il processo gira ma la salute non passa: resta la precedente", func(t *testing.T) {
		e.t = t
		// Porta su cui l'immagine non ascolta: il container è su ma la readiness non passa mai.
		t.Logf("PATCH porta alle %s", time.Now().Format("15:04:05.000"))
		e.want(e.do(http.MethodPatch, path, "alice", `{"port":8081}`), http.StatusOK, "")
		v := e.waitState(path, 6, "failed")
		if !strings.Contains(deref(v.StateReason), "salute") {
			t.Fatalf("motivo %q", deref(v.StateReason))
		}
		if a := e.getApp(path); a.ActiveVersion != 3 {
			t.Fatalf("versione attiva %d", a.ActiveVersion)
		}
		e.assertNoLoss(tr, "salute non passa")
	})

	ok, lost, first := tr.finish()
	if lost != 0 || ok < 300 {
		t.Fatalf("traffico continuo: %d riuscite, %d perse (%s)", ok, lost, first)
	}
	t.Logf("traffico continuo: %d richieste, 0 perse", ok)

	t.Run("eliminata l'app il controller toglie il namespace", func(t *testing.T) {
		e.t = t
		e.want(e.do(http.MethodDelete, path, "alice", ""), http.StatusNoContent, "")
		deadline := time.Now().Add(3 * time.Minute)
		for {
			_, err := e.kube.CoreV1().Namespaces().Get(context.Background(), e.ns, metav1.GetOptions{})
			if err != nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("il namespace dell'app eliminata esiste ancora")
			}
			time.Sleep(2 * time.Second)
		}
	})
}
