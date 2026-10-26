//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

type h8NoopController struct{}

func (h8NoopController) Kick()                                    {}
func (h8NoopController) Restart(context.Context, store.App) error { return nil }
func (h8NoopController) Scale(context.Context, store.App) error   { return nil }

// GIT-373 (H8): i due endpoint verificano l'assenza prima di creare una versione. Il controller
// no-op basta agli handler e mantiene questa prova indipendente da un cluster Kubernetes.
func TestApps_DeployERollbackRifiutanoSegretiMancanti(t *testing.T) {
	e := newAppEnvWithKeysAndController(t, h8NoopController{})
	e.repo("secrets", true)
	base := "/repos/alice/secrets/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/secrets/apps", "alice", createWeb), http.StatusAccepted, "")
	a := e.app(e.do(http.MethodGet, base, "alice", ""))
	appID := uuid.UUID(a.Id)
	st := store.New(e.pool)
	if err := st.FinishAppDeploy(context.Background(), appID, 1, "serving", "", e.clock.Now()); err != nil {
		t.Fatal(err)
	}

	e.want(e.do(http.MethodPut, base+"/secrets/TOKEN", "alice", `{"value":"first-secret-value"}`), http.StatusOK, "")
	if err := st.FinishAppDeploy(context.Background(), appID, 2, "serving", "", e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.want(e.do(http.MethodDelete, base+"/secrets/TOKEN", "alice", ""), http.StatusNoContent, "")
	if err := st.FinishAppDeploy(context.Background(), appID, 3, "serving", "", e.clock.Now()); err != nil {
		t.Fatal(err)
	}

	// Il DELETE crea la versione 3 senza TOKEN. Per coprire POST /deploys verso una versione che
	// elenca TOKEN ma il cui valore e' stato rimosso, la fixture ne riporta la configurazione alla 2.
	e.sql(`UPDATE core.app_versions SET config = (
		SELECT config FROM core.app_versions WHERE app_id = $1 AND number = 2
	) WHERE app_id = $1 AND number = 3`, appID)
	var beforeActive, beforeNext, beforeVersions int
	if err := e.pool.QueryRow(context.Background(), `SELECT active_version, next_version FROM core.apps WHERE id = $1`, appID).Scan(&beforeActive, &beforeNext); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions WHERE app_id = $1`, appID).Scan(&beforeVersions); err != nil {
		t.Fatal(err)
	}
	assertMissing := func(recBody string) {
		t.Helper()
		if !strings.Contains(recBody, `"names":["TOKEN"]`) {
			t.Fatalf("409 senza il nome TOKEN: %s", recBody)
		}
	}

	rec := e.do(http.MethodPost, base+"/rollback", "alice", `{"version":2}`)
	e.want(rec, http.StatusConflict, "app_secrets_missing")
	assertMissing(rec.Body.String())
	rec = e.do(http.MethodPost, base+"/deploys", "alice", `{"image":"sha-7f3a91c"}`)
	e.want(rec, http.StatusConflict, "app_secrets_missing")
	assertMissing(rec.Body.String())

	var active, next, versions int
	if err := e.pool.QueryRow(context.Background(), `SELECT active_version, next_version FROM core.apps WHERE id = $1`, appID).Scan(&active, &next); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions WHERE app_id = $1`, appID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if active != beforeActive || next != beforeNext || versions != beforeVersions {
		t.Fatalf("409 ha modificato le versioni: active %d->%d, next %d->%d, righe %d->%d", beforeActive, active, beforeNext, next, beforeVersions, versions)
	}
}

// GIT-373 (H8): il controller porta nei pod soltanto un riferimento al Secret Kubernetes; i
// rollback usano i valori correnti e rifiutano i nomi eliminati prima di cambiare il cluster.
func TestApps_SegretiKubernetesERollback(t *testing.T) {
	e := newDeployEnv(t)

	e.repo("secrets", true)
	base := "/repos/alice/secrets/apps"
	const image = "docker.io/traefik/whoami:v1.10.1"
	rec := e.do(http.MethodPost, base, "alice", `{"name":"web","image":"`+image+`","port":80,"healthCheck":{"path":"/","intervalSeconds":1}}`)
	e.want(rec, http.StatusAccepted, "")
	appPath := base + "/web"
	app := e.waitApp(appPath, "prima versione", func(a openapi.App) bool { return a.Status == "running" })
	e.ns = apps.Namespace(uuid.UUID(app.Id))
	t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), e.ns, metav1.DeleteOptions{}) })

	put := func(value string) {
		e.want(e.do(http.MethodPut, appPath+"/secrets/TOKEN", "alice", `{"value":"`+value+`"}`), http.StatusOK, "")
	}
	secret := func() string {
		s, err := e.kube.CoreV1().Secrets(e.ns).Get(context.Background(), "app-secrets", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return string(s.Data["TOKEN"])
	}
	deployment := func() *appsv1.Deployment {
		d, err := e.kube.AppsV1().Deployments(e.ns).Get(context.Background(), apps.WorkloadName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	const first, rotated = "first-secret-value", "rotated-secret-value"
	put(first)
	e.waitState(appPath, 2, "serving")
	if got := secret(); got != first {
		t.Fatalf("Secret iniziale: %q", got)
	}
	d := deployment()
	container := d.Spec.Template.Spec.Containers[0]
	if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef == nil || container.EnvFrom[0].SecretRef.Name != "app-secrets" {
		t.Fatalf("Deployment senza envFrom del Secret: %+v", container.EnvFrom)
	}
	if strings.Contains(fmt.Sprintf("%+v", d.Spec), first) || strings.Contains(fmt.Sprintf("%+v", d.Spec), rotated) {
		t.Fatal("il Deployment contiene un valore di segreto in chiaro")
	}

	put(rotated)
	e.waitState(appPath, 3, "serving")
	if got := secret(); got != rotated {
		t.Fatalf("Secret dopo rotazione: %q", got)
	}

	// La versione 2 conserva il nome TOKEN ma il rollback deve iniettare il valore ruotato.
	e.want(e.do(http.MethodPost, appPath+"/rollback", "alice", `{"version":2}`), http.StatusAccepted, "")
	e.waitState(appPath, 4, "serving")
	if got := secret(); got != rotated {
		t.Fatalf("rollback ha ripristinato un valore storico: %q", got)
	}

	e.want(e.do(http.MethodDelete, appPath+"/secrets/TOKEN", "alice", ""), http.StatusNoContent, "")
	e.waitState(appPath, 5, "serving")
	if _, err := e.kube.CoreV1().Secrets(e.ns).Get(context.Background(), "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("il Secret dell'app senza nomi deve essere rimosso")
	}
	before := e.getApp(appPath)
	rec = e.do(http.MethodPost, appPath+"/rollback", "alice", `{"version":2}`)
	e.want(rec, http.StatusConflict, "app_secrets_missing")
	if !strings.Contains(rec.Body.String(), "TOKEN") {
		t.Fatalf("409 senza nome mancante: %s", rec.Body.String())
	}
	after := e.getApp(appPath)
	if after.ActiveVersion != before.ActiveVersion {
		t.Fatalf("versione attiva cambiata dopo 409: %d -> %d", before.ActiveVersion, after.ActiveVersion)
	}
	if _, err := e.kube.CoreV1().Secrets(e.ns).Get(context.Background(), "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("409 ha ricreato il Secret Kubernetes")
	}

	e.want(e.do(http.MethodDelete, appPath, "alice", ""), http.StatusNoContent, "")
	// Il namespace sparisce solo dopo il grace period del pod (30 s) e i finalizer.
	deadline := time.Now().Add(150 * time.Second)
	for {
		_, err := e.kube.CoreV1().Namespaces().Get(context.Background(), e.ns, metav1.GetOptions{})
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("il namespace %s dell'app non e' sparito entro 150 s dal DELETE (pod in grace period o finalizer): il Secret Kubernetes non e' stato eliminato", e.ns)
		}
		time.Sleep(time.Second)
	}
}
