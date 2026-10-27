package apps

import (
	"context"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func syncTestApp() (store.App, store.AppVersion) {
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 2, Status: "running", ReadyCopies: 1, Config: store.AppConfig{Copies: 1}}
	v := store.AppVersion{AppID: app.ID, Number: 2, State: "serving", Image: store.AppImage{Reference: "registry.gitstack.test/alice/web:v2", Source: "registry"}}
	return app, v
}

func countCreates(kube *fake.Clientset, resource string) int {
	n := 0
	for _, a := range kube.Actions() {
		if a.Matches("create", resource) {
			n++
		}
	}
	return n
}

// Dopo un restore su un cluster pulito il namespace dell'app esiste (creato dal restore per i volumi)
// ma senza il RoleBinding di core: il Get del Deployment risponde Forbidden, non NotFound. Il sync
// deve riapplicare lo stesso (namespace, RoleBinding, Deployment), non fermarsi in silenzio (GIT-357).
func TestSyncApp_DeploymentForbiddenRiapplicaConRoleBinding(t *testing.T) {
	app, v := syncTestApp()
	kube := fake.NewClientset()
	forbidden := true
	kube.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if forbidden {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, WorkloadName, nil)
		}
		return false, nil, nil
	})
	// Dopo la creazione del RoleBinding i permessi ci sono: il Get torna a funzionare.
	kube.PrependReactor("create", "rolebindings", func(k8stesting.Action) (bool, runtime.Object, error) {
		forbidden = false
		return false, nil, nil
	})
	c := New(nil, kube, nil, Config{ControllerServiceAccount: "gitstack/gitstack-core", WorkloadRole: "gitstack-app-workload"})
	c.versionOf = func(context.Context, store.App) (store.AppVersion, error) { return v, nil }

	c.syncApp(context.Background(), app)

	ns := Namespace(app.ID)
	if _, err := kube.RbacV1().RoleBindings(ns).Get(context.Background(), RoleBindingName, metav1.GetOptions{}); err != nil {
		t.Fatalf("RoleBinding nel namespace dell'app: %v", err)
	}
	if _, err := kube.AppsV1().Deployments(ns).Get(context.Background(), WorkloadName, metav1.GetOptions{}); err != nil {
		t.Fatalf("Deployment non creato: %v", err)
	}
	if _, err := kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace: %v", err)
	}
}

// Con il Deployment presente e leggibile il sync non riapplica: nessun Create del Deployment.
func TestSyncApp_DeploymentLeggibileNonRiapplica(t *testing.T) {
	app, v := syncTestApp()
	one := int32(1)
	kube := fake.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app.ID)},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
		Status:     appsv1.DeploymentStatus{Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	})
	c := New(nil, kube, nil, Config{ControllerServiceAccount: "gitstack/gitstack-core", WorkloadRole: "gitstack-app-workload"})
	reapplied := false
	c.versionOf = func(context.Context, store.App) (store.AppVersion, error) { reapplied = true; return v, nil }

	c.syncApp(context.Background(), app)

	if reapplied || countCreates(kube, "deployments") != 0 {
		t.Fatalf("riapplicazione inattesa: versionOf=%v, create di Deployment=%d", reapplied, countCreates(kube, "deployments"))
	}
}

// Un errore diverso da NotFound e Forbidden non riapplica (lo registra e basta).
func TestSyncApp_AltroErroreNonRiapplica(t *testing.T) {
	app, v := syncTestApp()
	kube := fake.NewClientset()
	kube.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(context.DeadlineExceeded)
	})
	c := New(nil, kube, nil, Config{})
	reapplied := false
	c.versionOf = func(context.Context, store.App) (store.AppVersion, error) { reapplied = true; return v, nil }
	c.syncApp(context.Background(), app)
	if reapplied || countCreates(kube, "deployments") != 0 {
		t.Fatal("un errore generico non deve riapplicare")
	}
}
