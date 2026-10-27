package apps

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Il deployment controller di Kubernetes aggiorna lo stesso Deployment mentre l'utente fa un nuovo
// deploy: l'Update col ResourceVersion vecchio riceve 409 e va riprovato rileggendo l'oggetto.
func conflict() error {
	return apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, WorkloadName,
		errConflict)
}

type conflictErr struct{}

func (conflictErr) Error() string { return "the object has been modified" }

var errConflict error = conflictErr{}

func conflictFixture(t *testing.T) (*Controller, *fake.Clientset, store.App, store.AppVersion) {
	t.Helper()
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 2, Config: store.AppConfig{Copies: 1}}
	v := store.AppVersion{AppID: app.ID, Number: 2, State: "serving", Image: store.AppImage{Reference: "registry.gitstack.test/alice/web:v2", Source: "registry"}}
	kube := fake.NewClientset()
	c := New(nil, kube, nil, Config{})
	if err := c.apply(context.Background(), app, v); err != nil {
		t.Fatal(err)
	}
	return c, kube, app, v
}

func TestApply_RiprovaSuConflittoDelDeployment(t *testing.T) {
	c, kube, app, v := conflictFixture(t)
	var updates, gets atomic.Int32
	kube.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets.Add(1)
		return false, nil, nil
	})
	kube.PrependReactor("update", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if updates.Add(1) == 1 {
			return true, nil, conflict()
		}
		return false, nil, nil
	})
	v.Image.Reference = "registry.gitstack.test/alice/web:v3"
	if err := c.apply(context.Background(), app, v); err != nil {
		t.Fatalf("il deploy deve riuscire al secondo tentativo: %v", err)
	}
	if updates.Load() != 2 {
		t.Errorf("Update = %d, attesi 2", updates.Load())
	}
	if gets.Load() < 2 {
		t.Errorf("Get = %d: l'oggetto va riletto a ogni tentativo", gets.Load())
	}
}

func TestApply_ConflittiContinuiRestituisconoErrore(t *testing.T) {
	c, kube, app, v := conflictFixture(t)
	var updates atomic.Int32
	kube.PrependReactor("update", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates.Add(1)
		return true, nil, conflict()
	})
	err := c.apply(context.Background(), app, v)
	if !apierrors.IsConflict(err) {
		t.Fatalf("err = %v, atteso il Conflict dopo i tentativi", err)
	}
	if n := updates.Load(); n < 2 || n > 10 {
		t.Errorf("Update = %d: i tentativi devono essere limitati", n)
	}
}
