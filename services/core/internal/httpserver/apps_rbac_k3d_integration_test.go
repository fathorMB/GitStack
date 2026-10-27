//go:build integration

package httpserver_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-424: il primo deploy di un'app SENZA segreti passa da deleteAppSecret (Delete su un Secret
// che non esiste). Con il ClusterRole del chart privo di `delete` sui secrets, RBAC rifiutava la
// richiesta prima di guardare se il Secret c'era e il deploy finiva in failed. Gli altri test k3d
// usano il kubeconfig cluster-admin e non vedevano il problema: qui il controller parla al
// cluster con il token del ServiceAccount di core e l'RBAC reso dal chart (k3d-test.sh).
func TestApps_SenzaSegretiConRbacDelChartSuK3d(t *testing.T) {
	kc := os.Getenv(envTestKubeconfigCore)
	if kc == "" {
		t.Skip(envTestKubeconfigCore + " non impostata: serve l'RBAC del chart su k3d (internal/apps/k3d-test.sh)")
	}
	e := newDeployEnvAs(t, true, kc)
	e.repo("norbac", true)
	base := "/repos/alice/norbac/apps"
	const image = "docker.io/traefik/whoami:v1.10.1"
	rec := e.do(http.MethodPost, base, "alice", `{"name":"web","image":"`+image+`","port":80,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	appPath := base + "/web"
	app := e.waitApp(appPath, "app running senza segreti", func(a openapi.App) bool { return a.Status == "running" })
	e.ns = apps.Namespace(uuid.UUID(app.Id))
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), e.ns, metav1.DeleteOptions{}) })

	v := e.waitState(appPath, 1, "serving")
	if v.State != "serving" {
		t.Fatalf("versione 1: %s %s", v.State, deref(v.StateReason))
	}
	if _, err := e.kube.CoreV1().Secrets(e.ns).Get(context.Background(), "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("app senza segreti: il Secret app-secrets non doveva esistere")
	}
}
