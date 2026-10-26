//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-379 (H15): dentro lo stesso repo ogni app si raggiunge con il proprio nome (http://api:<porta>).
// Il test crea due repo con un'app `api` ciascuno, e nel primo anche `site`: dal pod di `site` il
// nome `api` arriva all'api del suo repo (porta dichiarata e porta 80), anche dopo un nuovo deploy;
// l'api dell'altro repo non è raggiungibile. Una riga ok/ko per controllo.
func TestApps_NomiInterniSuK3d(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	const echo1, echo2 = "docker.io/ealen/echo-server:0.9.2", "docker.io/ealen/echo-server:latest"
	const hello = "docker.io/traefik/whoami:v1.10.1"

	create := func(repo, name, image string, port int) (uuid.UUID, string) {
		t.Helper()
		body := fmt.Sprintf(`{"name":%q,"image":%q,"port":%d,"healthCheck":{"path":"/","intervalSeconds":1}}`, name, image, port)
		rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/apps", "alice", body)
		e.want(rec, http.StatusAccepted, "")
		var created openapi.AppCreated
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		id := uuid.UUID(created.App.Id)
		t.Cleanup(func() {
			_ = e.kube.CoreV1().Namespaces().Delete(context.Background(), apps.Namespace(id), metav1.DeleteOptions{})
		})
		path := "/repos/alice/" + repo + "/apps/" + name
		e.waitState(path, 1, "serving")
		return id, path
	}
	e.repo("handbook", true)
	e.repo("other", true)
	apiID, apiPath := create("handbook", "api", echo1, 8080)
	siteID, _ := create("handbook", "site", hello, 80)
	otherID, _ := create("other", "api", hello, 80)
	siteNS := apps.Namespace(siteID)

	// L'alias di `api` nel namespace di `site` punta al Service dell'api dello stesso repo, mai all'altro.
	wantTarget := "app." + apps.Namespace(apiID) + ".svc.cluster.local"
	var alias *corev1.Service
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if s, err := e.kube.CoreV1().Services(siteNS).Get(ctx, "api", metav1.GetOptions{}); err == nil {
			alias = s
			break
		}
	}
	if alias == nil || alias.Spec.Type != corev1.ServiceTypeExternalName || alias.Spec.ExternalName != wantTarget {
		t.Fatalf("KO alias api in %s: %+v (atteso ExternalName %s)", siteNS, alias, wantTarget)
	}
	t.Logf("OK: alias api -> %s", wantTarget)
	if _, err := e.kube.CoreV1().Services(apps.Namespace(otherID)).Get(ctx, "site", metav1.GetOptions{}); err == nil {
		t.Fatal("KO: l'altro repo ha un alias verso site")
	}
	t.Log("OK: nessun alias verso app di altri repo")

	probeSeq := 0
	// probe lancia un pod nel namespace di site (label managed, come le app) e vuole OK nel log.
	probe := func(name, script string) {
		t.Helper()
		probeSeq++
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%d", name, probeSeq), Namespace: siteNS, Labels: map[string]string{apps.LabelManaged: "true"}},
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", script}}}}}
		if _, err := e.kube.CoreV1().Pods(siteNS).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := waitPodLog(t, e.kube, siteNS, pod.Name); got != "OK" {
			t.Fatalf("KO %s: %s", name, got)
		}
		t.Logf("OK: %s", name)
	}
	// get: la richiesta riesce (con tentativi, il DNS e l'avvio del pod richiedono qualche secondo).
	get := func(url string) string {
		return `for i in $(seq 1 30); do if wget -q -T 3 -O /tmp/body ` + url + `; then echo OK; exit 0; fi; sleep 1; done; echo KO; exit 1`
	}
	probe("http-api-porta-dichiarata", get("http://api:8080/"))
	probe("http-api-porta-80", get("http://api/"))

	// Nuovo deploy di api: il nome resta valido.
	e.want(e.do(http.MethodPost, apiPath+"/deploys", "alice", `{"image":"`+echo2+`"}`), http.StatusAccepted, "")
	e.waitState(apiPath, 2, "serving")
	e.waitApp(apiPath, "v2 a regime", func(a openapi.App) bool { return a.Status == "running" && a.ReadyCopies == 1 })
	probe("http-api-dopo-nuovo-deploy", get("http://api:8080/"))

	// Rollback alla versione 1: stesso nome, stesso alias.
	e.want(e.do(http.MethodPost, apiPath+"/rollback", "alice", `{"version":1}`), http.StatusAccepted, "")
	e.waitState(apiPath, 3, "serving")
	probe("http-api-dopo-rollback", get("http://api:8080/"))

	// L'api dell'altro repo ha lo stesso nome ma non è né risolta né raggiungibile dal pod di site.
	otherFQDN := "app." + apps.Namespace(otherID) + ".svc.cluster.local"
	probe("altro-repo-fqdn-bloccato", "if nc -z -w 3 "+otherFQDN+" 80; then echo KO; exit 1; fi; echo OK")
	probe("altro-repo-non-raggiunto-dal-nome", "wget -q -T 3 -O /tmp/body http://api:8080/ && grep -q Hostname /tmp/body && { echo KO; exit 1; }; echo OK")
}
