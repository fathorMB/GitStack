package apps

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// testGateImage: immagine del cancello per i test che applicano una versione (GIT-422).
const testGateImage = "registry.gitstack.test/gitstack-core:test"

// GIT-422: senza GITSTACK_CORE_APPS_GATE_IMAGE il deploy è rifiutato prima di toccare il cluster.
func TestApply_SenzaImmagineDelCancelloRifiuta(t *testing.T) {
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 1, Config: store.AppConfig{Copies: 1}}
	v := store.AppVersion{AppID: app.ID, Number: 1, State: "serving", Image: store.AppImage{Reference: "registry.gitstack.test/alice/web:v1", Source: "registry"}}
	kube := fake.NewClientset()
	c := New(nil, kube, nil, Config{})
	err := c.apply(context.Background(), app, v)
	if !errors.Is(err, ErrGateImageMissing) {
		t.Fatalf("err = %v, atteso ErrGateImageMissing", err)
	}
	if r := c.rollout(context.Background(), app, v); r == "" {
		t.Error("rollout deve riportare il motivo del rifiuto")
	}
	for _, a := range kube.Actions() {
		if a.Matches("create", "deployments") || a.Matches("update", "deployments") {
			t.Errorf("azione sui Deployment senza cancello: %v", a)
		}
	}
	if err := c.Restart(context.Background(), app); !errors.Is(err, ErrGateImageMissing) {
		t.Errorf("Restart = %v, atteso ErrGateImageMissing", err)
	}
	if err := c.Scale(context.Background(), app); !errors.Is(err, ErrGateImageMissing) {
		t.Errorf("Scale a 1 copia = %v, atteso ErrGateImageMissing", err)
	}
	app.Config.Copies = 0
	if err := c.Scale(context.Background(), app); errors.Is(err, ErrGateImageMissing) {
		t.Error("fermare l'app (0 copie) deve restare permesso")
	}
	ns := Namespace(app.ID)
	if _, err := kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err == nil {
		t.Error("nessun namespace va creato senza cancello")
	}
	if l, _ := kube.AppsV1().Deployments(ns).List(context.Background(), metav1.ListOptions{}); l != nil && len(l.Items) != 0 {
		t.Errorf("nessun Deployment senza cancello: %d", len(l.Items))
	}
}

func TestApply_ConImmagineDelCancelloCreaIlDeploymentConInitContainer(t *testing.T) {
	c, kube, app, _ := conflictFixture(t)
	d, err := kube.AppsV1().Deployments(Namespace(app.ID)).Get(context.Background(), WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ic := d.Spec.Template.Spec.InitContainers
	if len(ic) != 1 || ic[0].Image != testGateImage {
		t.Fatalf("init container = %+v", ic)
	}
	_ = c
}
