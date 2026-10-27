//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

const envTestTraefikMetrics = "GITSTACK_TEST_TRAEFIK_METRICS"

// GIT-403 (H12): su un k3d con la HelmChartConfig di Traefik (k3d-test.sh) un'app riceve traffico dal
// vero Traefik, con un 5xx, e GET /apps/{app}/metrics lo ritrova nelle serie requests ed errors5xx.
// Prova il formato reale dell'etichetta `service` delle IngressRoute (non solo il parsing). Il traffico
// parte da un pod curl nel cluster verso il Service di Traefik con SNI e Host dell'app; il forward-auth
// del login punta a un finto verificatore (whoami: risponde 200) perché senza gateway l'app `login`
// non avrebbe rotta. Le metriche di Traefik si leggono dal Service headless `traefik-metrics` tramite
// il proxy dell'API server: da fuori cluster la NetworkPolicy del chart non c'è.
func TestApps_RichiesteETraefikSuK3d(t *testing.T) {
	if os.Getenv(envTestTraefikMetrics) == "" {
		t.Skip(envTestTraefikMetrics + " non impostata: serve la HelmChartConfig di Traefik (internal/apps/k3d-test.sh)")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", os.Getenv("GITSTACK_TEST_KUBECONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const whoami = "docker.io/traefik/whoami:v1.10.1"

	// Finto verificatore del forward-auth: whoami risponde 200 a qualunque percorso.
	authLabels := map[string]string{"app": "gs-auth-stub"}
	authPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gs-auth-stub", Namespace: "default", Labels: authLabels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "w", Image: whoami, Ports: []corev1.ContainerPort{{ContainerPort: 80}}}}}}
	authSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "gs-auth-stub", Namespace: "default"},
		Spec: corev1.ServiceSpec{Selector: authLabels, Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(80)}}}}
	for _, o := range []func() error{
		func() error {
			_, err := kube.CoreV1().Pods("default").Create(ctx, authPod, metav1.CreateOptions{})
			return err
		},
		func() error {
			_, err := kube.CoreV1().Services("default").Create(ctx, authSvc, metav1.CreateOptions{})
			return err
		},
	} {
		if err := o(); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = kube.CoreV1().Pods("default").Delete(context.Background(), "gs-auth-stub", metav1.DeleteOptions{})
		_ = kube.CoreV1().Services("default").Delete(context.Background(), "gs-auth-stub", metav1.DeleteOptions{})
	})

	e := newDeployEnvWith(t, true, deployEnvOptions{tune: func(c *apps.Config) {
		c.MetricsEvery = time.Second
		c.TraefikNamespace = "kube-system"
		c.AuthURL = "http://gs-auth-stub.default.svc.cluster.local/_appauth/verify"
		c.AuthSecret = "segreto-di-prova"
		c.TraefikMetrics = func(ctx context.Context) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return kube.CoreV1().Services("kube-system").ProxyGet("http", "traefik-metrics", "9100", "/metrics", nil).DoRaw(ctx)
		}
	}})

	e.repo("handbook", true)
	rec := e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"web","image":"`+whoami+`","port":80,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	ns := apps.Namespace(uuid.UUID(created.App.Id))
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	path := "/repos/alice/handbook/apps/web"
	e.waitState(path, 1, "serving")
	host := strings.TrimPrefix(deref(e.getApp(path).Address), "https://")
	if host == "" {
		t.Fatal("KO: l'app non ha indirizzo, quindi nessuna rotta")
	}

	series := func() openapi.AppMetrics {
		var m openapi.AppMetrics
		rec := e.do(http.MethodGet, path+"/metrics?range=1h", "alice", "")
		e.want(rec, http.StatusOK, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	total := func(s *openapi.AppMetricSeries) (n float32) {
		if s == nil {
			return 0
		}
		for _, p := range s.Points {
			n += p.Value
		}
		return n
	}

	// Il primo campione (base a zero) dice che la sorgente risponde: le serie non sono più null.
	deadline := time.Now().Add(2 * time.Minute)
	for series().Requests == nil {
		if time.Now().After(deadline) {
			t.Fatal("KO: nessun campione di Traefik in 2 minuti: la sorgente (traefik-metrics:9100) non risponde?")
		}
		time.Sleep(time.Second)
	}
	t.Log("ok: le serie requests ed errors5xx non sono null (Traefik risponde)")

	// Traffico dal cluster verso Traefik: 20 GET (200) e 3 POST /health con corpo 500 (whoami risponde 500).
	traefikSvc, err := kube.CoreV1().Services("kube-system").Get(ctx, "traefik", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range traefikSvc.Spec.Ports {
		if p.Port == 9100 || p.TargetPort.IntValue() == 9100 {
			t.Fatalf("KO: l'entrypoint metrics e' esposto sul Service LoadBalancer di Traefik: %+v", p)
		}
	}
	script := fmt.Sprintf(`set -u
ip=%s
for i in $(seq 1 20); do curl -sk -o /dev/null -w 'get %%{http_code}\n' --resolve %s:443:$ip https://%s/; done
for i in 1 2 3; do curl -sk -o /dev/null -w 'post %%{http_code}\n' -X POST -d 500 --resolve %s:443:$ip https://%s/health; done`,
		traefikSvc.Spec.ClusterIP, host, host, host, host)
	curl := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gs-traffic", Namespace: "default"},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "curl", Image: "docker.io/curlimages/curl:8.10.1", Command: []string{"sh", "-c", script}}}}}
	if _, err := kube.CoreV1().Pods("default").Create(ctx, curl, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = kube.CoreV1().Pods("default").Delete(context.Background(), "gs-traffic", metav1.DeleteOptions{})
	})
	out := waitPodLog(t, kube, "default", "gs-traffic")
	t.Logf("risposte viste dal client:\n%s", out)
	if strings.Count(out, "get 200") != 20 || strings.Count(out, "post 500") != 3 {
		t.Fatalf("KO: il traffico non e' arrivato all'app (volute 20 risposte 200 e 3 risposte 500)")
	}

	// Le serie ritrovano il traffico: almeno 23 richieste e 3 errori 5xx (il forward-auth e le sonde
	// non passano dal service dell'app).
	var m openapi.AppMetrics
	deadline = time.Now().Add(90 * time.Second)
	for {
		m = series()
		if total(m.Requests) >= 23 && total(m.Errors5xx) >= 3 {
			break
		}
		if time.Now().After(deadline) {
			text, _ := kube.CoreV1().Services("kube-system").ProxyGet("http", "traefik-metrics", "9100", "/metrics", nil).DoRaw(ctx)
			var lines []string
			for _, l := range strings.Split(string(text), "\n") {
				if strings.HasPrefix(l, "traefik_service_requests_total") {
					lines = append(lines, l)
				}
			}
			t.Fatalf("KO: dopo 23 richieste e 3 errori 5xx le serie hanno %v richieste e %v errori. Righe di Traefik per service:\n%s",
				total(m.Requests), total(m.Errors5xx), strings.Join(lines, "\n"))
		}
		time.Sleep(time.Second)
	}
	if total(m.Errors5xx) > total(m.Requests) {
		t.Fatalf("KO: piu' errori (%v) che richieste (%v)", total(m.Errors5xx), total(m.Requests))
	}
	t.Logf("ok: %v richieste e %v errori 5xx ritrovati nelle serie dell'app", total(m.Requests), total(m.Errors5xx))
}
