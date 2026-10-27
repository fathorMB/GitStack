//go:build integration

package httpserver_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-425: un'app di un repo PRIVATO con l'immagine nel registry del repo arriva a serving su un
// cluster vero senza configurazioni a mano. Si prova con services/core/internal/apps/k3d-pull-test.sh:
//   - il registry è un registry:2 vero, dietro un proxy TLS con la CA di prova che fa quello che fa il
//     servizio git: chiede al ci-gateway di core (cigateway, registry-access, il codice vero) cosa può
//     fare il token Basic e lascia passare solo il pull del repo dell'app;
//   - containerd si fida della CA grazie a registries.yaml generato da install.sh (scenario «x509» =
//     senza, per il controllo negativo);
//   - core crea il pull secret con apps.PullCredentialsFor (scenario «auth» = senza credenziali, per il
//     controllo negativo).
//
// Variabili: GITSTACK_TEST_PULL_PROXY_ADDR (host:porta con cui i nodi raggiungono il proxy, es.
// host.k3d.internal:5543), _PULL_UPSTREAM (URL del registry:2), _PULL_CERT_DIR (server.crt e server.key),
// _PULL_IMAGE (nome sotto l'host: alice/handbook/e2eimg:v1), _PULL_SCENARIO (x509 | auth | ok).
func TestApps_RegistryPrivatoSuK3d(t *testing.T) {
	proxyAddr := os.Getenv("GITSTACK_TEST_PULL_PROXY_ADDR")
	if proxyAddr == "" {
		t.Skip("GITSTACK_TEST_PULL_PROXY_ADDR non impostata: servono k3d e registry (internal/apps/k3d-pull-test.sh)")
	}
	scenario := os.Getenv("GITSTACK_TEST_PULL_SCENARIO")
	upstream, err := url.Parse(os.Getenv("GITSTACK_TEST_PULL_UPSTREAM"))
	if err != nil || upstream.Host == "" {
		t.Fatalf("GITSTACK_TEST_PULL_UPSTREAM: %v", err)
	}
	image := proxyAddr + "/" + os.Getenv("GITSTACK_TEST_PULL_IMAGE")
	keys := []string{"chiave-del-gateway-di-prova"}

	withCreds := scenario != "auth"
	e := newDeployEnvWith(t, true, deployEnvOptions{
		publicURL: "https://" + proxyAddr,
		tune: func(c *apps.Config) {
			if withCreds {
				c.PullCredentials = apps.PullCredentialsFor(keys, time.Now)
			}
		},
	})
	repoID := e.repo("handbook", false) // PRIVATO
	_ = repoID

	// Il ci-gateway vero, con le chiavi di prova: è lui che dice cosa può fare il token dell'app.
	gw := httptest.NewServer(cigateway.New(cigateway.Config{Pool: e.pool, AppPullKeys: keys}))
	defer gw.Close()

	var mu sync.Mutex
	var seen []string // «sì/no» per richiesta, per il riepilogo
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	front := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deny := func(code int, why string) {
			mu.Lock()
			seen = append(seen, fmt.Sprintf("%s %s -> %d (%s)", r.Method, r.URL.Path, code, why))
			mu.Unlock()
			w.Header().Set("WWW-Authenticate", `Basic realm="GitStack registry"`)
			http.Error(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required: `+why+`"}]}`, code)
		}
		_, token, ok := r.BasicAuth()
		if !ok {
			deny(http.StatusUnauthorized, "no basic auth credentials")
			return
		}
		// /v2/ è il controllo di versione; il resto è /v2/<owner>/<repo>/<nome>/...
		owner, repo := "alice", "handbook"
		if rest := strings.TrimPrefix(r.URL.Path, "/v2/"); rest != "" && rest != r.URL.Path {
			if parts := strings.Split(rest, "/"); len(parts) >= 3 {
				owner, repo = parts[0], parts[1]
			}
		}
		req, _ := http.NewRequest(http.MethodGet, gw.URL+cigateway.RegistryAccessPath+"?owner="+owner+"&repo="+repo, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			deny(http.StatusServiceUnavailable, err.Error())
			return
		}
		defer func() { _ = res.Body.Close() }()
		var acc cigateway.RegistryAccess
		if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&acc) != nil {
			deny(http.StatusUnauthorized, fmt.Sprintf("registry-access %d", res.StatusCode))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead || acc.Packages != "read" {
			deny(http.StatusForbidden, "solo pull")
			return
		}
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%s %s -> ok (packages=%s, repo=%s/%s)", r.Method, r.URL.Path, acc.Packages, acc.Owner, acc.Name))
		mu.Unlock()
		proxy.ServeHTTP(w, r)
	})
	cert, err := tls.LoadX509KeyPair(filepath.Join(os.Getenv("GITSTACK_TEST_PULL_CERT_DIR"), "server.crt"), filepath.Join(os.Getenv("GITSTACK_TEST_PULL_CERT_DIR"), "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(proxyAddr)
	ln, err := tls.Listen("tcp", "0.0.0.0:"+port, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: front, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	base := "/repos/alice/handbook/apps"
	rec := e.do(http.MethodPost, base, "alice", `{"name":"web","image":"`+image+`","port":8080,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	e.ns = apps.Namespace(uuid.UUID(created.App.Id))
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), e.ns, metav1.DeleteOptions{}) })
	path := base + "/web"

	summary := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(seen, "\n   ")
	}
	podWaiting := func() string {
		pods, _ := e.kube.CoreV1().Pods(e.ns).List(context.Background(), metav1.ListOptions{})
		var out []string
		for _, p := range pods.Items {
			for _, cs := range p.Status.InitContainerStatuses {
				if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.ExitCode != 0 {
					raw, _ := e.kube.CoreV1().Pods(e.ns).GetLogs(p.Name, &corev1.PodLogOptions{Container: cs.Name, Previous: true}).DoRaw(context.Background())
					out = append(out, "init "+cs.Name+": "+strings.TrimSpace(string(raw)))
				}
			}
			for _, cs := range p.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					out = append(out, cs.State.Waiting.Reason+": "+cs.State.Waiting.Message)
				}
			}
		}
		evs, _ := e.kube.CoreV1().Events(e.ns).List(context.Background(), metav1.ListOptions{})
		for _, ev := range evs.Items {
			if ev.Reason == "Failed" || ev.Reason == "BackOff" {
				out = append(out, ev.Reason+": "+ev.Message)
			}
		}
		return strings.Join(out, " | ")
	}

	switch scenario {
	case "x509", "auth":
		// Controllo negativo: il deploy non arriva a serving e il motivo è quello atteso.
		want := map[string]string{"x509": "x509", "auth": "authorization failed"}[scenario]
		deadline := time.Now().Add(3 * time.Minute)
		for {
			if w := podWaiting(); strings.Contains(w, want) {
				t.Logf("PROVA %s: il pod non scarica l'immagine, come previsto: %s", scenario, w)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("scenario %s: atteso «%s», trovato «%s»", scenario, want, podWaiting())
			}
			time.Sleep(2 * time.Second)
		}
		if st := e.version(path, 1).State; st == "serving" {
			t.Fatalf("scenario %s: la versione è «serving»", scenario)
		}
	default:
		sec, err := waitSecret(e, "registry-pull")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Secret %s/registry-pull tipo %s, chiave .dockerconfigjson di %d byte", e.ns, sec.Type, len(sec.Data[".dockerconfigjson"]))
		v := e.waitState(path, 1, "serving")
		a := e.waitApp(path, "app running", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 1 })
		d, _ := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if len(d.Spec.Template.Spec.ImagePullSecrets) != 1 || d.Spec.Template.Spec.Containers[0].Image != image {
			t.Fatalf("Deployment: %+v", d.Spec.Template.Spec)
		}
		t.Logf("PROVA ok: versione %d %s, app %s con %d copia pronta, immagine %s", v.Number, v.State, a.Status, a.ReadyCopies, image)
	}
	t.Logf("richieste al registry:\n   %s", summary())
}

func waitSecret(e *appsDeployEnv, name string) (*corev1.Secret, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		s, err := e.kube.CoreV1().Secrets(e.ns).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			return s, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(time.Second)
	}
}
