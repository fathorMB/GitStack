//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// execIn esegue un comando nel container dell'app e ne ritorna lo stdout.
func (e *appsDeployEnv) execIn(pod string, cmd ...string) string {
	e.t.Helper()
	rc, err := clientcmd.BuildConfigFromFlags("", os.Getenv("GITSTACK_TEST_KUBECONFIG"))
	if err != nil {
		e.t.Fatal(err)
	}
	req := e.kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(e.ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: "app", Command: cmd, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	ex, err := remotecommand.NewSPDYExecutor(rc, http.MethodPost, req.URL())
	if err != nil {
		e.t.Fatal(err)
	}
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: &errb}); err != nil {
		e.t.Fatalf("exec %v in %s: %v (%s)", cmd, pod, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// runningPod dà il nome dell'unico pod dell'app in esecuzione e non in chiusura.
func (e *appsDeployEnv) runningPod() string {
	e.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		l, err := e.kube.CoreV1().Pods(e.ns).List(context.Background(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + apps.WorkloadName})
		if err != nil {
			e.t.Fatal(err)
		}
		var names []string
		for _, p := range l.Items {
			if p.DeletionTimestamp == nil && p.Status.Phase == corev1.PodRunning {
				names = append(names, p.Name)
			}
		}
		if len(names) == 1 && len(l.Items) == 1 {
			return names[0]
		}
		time.Sleep(time.Second)
	}
	e.t.Fatal("nessun pod unico in esecuzione")
	return ""
}

// fetch legge un percorso dal Service dell'app (via API server), riprovando finché risponde.
func (e *appsDeployEnv) fetch(path string) string {
	e.t.Helper()
	var last error
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		b, err := e.kube.CoreV1().Services(e.ns).ProxyGet("http", apps.WorkloadName, "80", path, nil).DoRaw(ctx)
		cancel()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
		last = err
		time.Sleep(time.Second)
	}
	e.t.Fatalf("GET %s: %v", path, last)
	return ""
}

// Su k3d (H3): un'app con volume scrive, viene riavviata e ritrova i dati; un deploy di una nuova
// versione non perde nulla; la scala a 2 è rifiutata; il volume tolto elimina il PVC.
func TestApps_VolumiSuK3d(t *testing.T) {
	e := newDeployEnv(t)
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps"
	const img1, img2 = "docker.io/nginxinc/nginx-unprivileged:1.27-alpine", "docker.io/nginxinc/nginx-unprivileged:1.27.5-alpine"

	rec := e.do(http.MethodPost, base, "alice", `{"name":"wiki","image":"`+img1+`","port":8080,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	e.ns = apps.Namespace(uuid.UUID(created.App.Id))
	path := base + "/wiki"
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), e.ns, metav1.DeleteOptions{}) })
	e.waitState(path, 1, "serving")

	// Il volume si monta sotto la radice web di nginx: quello che l'app scrive lì lo serve anche.
	const mount = "/usr/share/nginx/html/data"
	rec = e.do(http.MethodPost, path+"/volumes", "alice", volumeBody("dati", mount, gib))
	e.want(rec, http.StatusAccepted, "")
	e.waitState(path, 2, "serving")

	d, err := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("con un volume l'aggiornamento è Recreate, trovato %s", d.Spec.Strategy.Type)
	}
	pvc, err := e.kube.CoreV1().PersistentVolumeClaims(e.ns).Get(context.Background(), "vol-dati", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("PVC: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "local-path" || pvc.Status.Phase != corev1.ClaimBound {
		t.Fatalf("PVC non su local-path o non legato: %+v", pvc.Status)
	}

	t.Run("scrive, il riavvio ritrova i dati", func(t *testing.T) {
		e.t = t
		pod := e.runningPod()
		e.execIn(pod, "sh", "-c", "echo dato-persistente-1 > "+mount+"/marker.txt && sync")
		if got := e.fetch("/data/marker.txt"); got != "dato-persistente-1" {
			t.Fatalf("l'app non serve quanto ha scritto: %q", got)
		}
		e.want(e.do(http.MethodPost, path+"/restart", "alice", ""), http.StatusAccepted, "")
		deadline := time.Now().Add(3 * time.Minute)
		var next string
		for {
			if time.Now().After(deadline) {
				t.Fatal("il riavvio non ha sostituito il pod")
			}
			l, _ := e.kube.CoreV1().Pods(e.ns).List(context.Background(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + apps.WorkloadName})
			next = ""
			if len(l.Items) == 1 && l.Items[0].Name != pod && l.Items[0].Status.Phase == corev1.PodRunning && l.Items[0].DeletionTimestamp == nil {
				next = l.Items[0].Name
				break
			}
			time.Sleep(time.Second)
		}
		t.Logf("pod prima: %s, dopo il riavvio: %s", pod, next)
		e.waitApp(path, "app running", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 1 })
		if got := e.fetch("/data/marker.txt"); got != "dato-persistente-1" {
			t.Fatalf("dopo il riavvio i dati non ci sono: %q", got)
		}
		if got := e.execIn(next, "cat", mount+"/marker.txt"); got != "dato-persistente-1" {
			t.Fatalf("dentro il nuovo pod: %q", got)
		}
	})

	t.Run("deploy di una nuova versione senza perdita", func(t *testing.T) {
		e.t = t
		before := e.runningPod()
		e.want(e.do(http.MethodPost, path+"/deploys", "alice", `{"image":"`+img2+`"}`), http.StatusAccepted, "")
		e.waitState(path, 3, "serving")
		after := e.runningPod()
		if after == before {
			t.Fatalf("il deploy doveva sostituire il pod %s", before)
		}
		d, _ := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if d.Spec.Template.Spec.Containers[0].Image != img2 {
			t.Fatalf("immagine: %s", d.Spec.Template.Spec.Containers[0].Image)
		}
		if got := e.fetch("/data/marker.txt"); got != "dato-persistente-1" {
			t.Fatalf("dopo il deploy i dati non ci sono: %q", got)
		}
		// Il rollback alla versione senza volumi (la 1) non toglie il volume: i dati sono dati.
		e.want(e.do(http.MethodPost, path+"/rollback", "alice", `{"version":1}`), http.StatusAccepted, "")
		e.waitState(path, 4, "serving")
		if got := e.fetch("/data/marker.txt"); got != "dato-persistente-1" {
			t.Fatalf("dopo il rollback i dati non ci sono: %q", got)
		}
		if _, err := e.kube.CoreV1().PersistentVolumeClaims(e.ns).Get(context.Background(), "vol-dati", metav1.GetOptions{}); err != nil {
			t.Fatalf("il rollback non deve togliere il PVC: %v", err)
		}
	})

	t.Run("scala a 2 rifiutata", func(t *testing.T) {
		e.t = t
		rec := e.do(http.MethodPut, path+"/scale", "alice", `{"copies":2}`)
		e.want(rec, http.StatusConflict, "single_copy")
		if !strings.Contains(rec.Body.String(), "una sola copia") {
			t.Fatalf("messaggio: %s", rec.Body.String())
		}
	})

	t.Run("il volume tolto elimina il PVC", func(t *testing.T) {
		e.t = t
		e.want(e.do(http.MethodDelete, path+"/volumes/dati", "alice", ""), http.StatusAccepted, "")
		e.waitState(path, 5, "serving")
		deadline := time.Now().Add(90 * time.Second)
		for {
			_, err := e.kube.CoreV1().PersistentVolumeClaims(e.ns).Get(context.Background(), "vol-dati", metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("PVC ancora presente: %v", err)
			}
			time.Sleep(time.Second)
		}
		d, _ := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if d.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
			t.Fatalf("senza volumi si torna a RollingUpdate: %s", d.Spec.Strategy.Type)
		}
		// Senza volumi la scala a 2 torna ammessa.
		e.want(e.do(http.MethodPut, path+"/scale", "alice", `{"copies":2}`), http.StatusOK, "")
	})
}
