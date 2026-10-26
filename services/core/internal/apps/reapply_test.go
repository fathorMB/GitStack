package apps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Dopo un restore su un cluster pulito il Deployment dell'app non c'è, ma il database ha la
// versione attiva in servizio: il controller la riapplica (H4). Qui si provano la regola e
// l'applicazione con un cluster finto.
func TestShouldReapply(t *testing.T) {
	serving := store.AppVersion{Number: 2, State: "serving"}
	archived := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		app  store.App
		v    store.AppVersion
		verr error
		want bool
	}{
		{"versione in servizio con copie", store.App{Status: "running", Config: store.AppConfig{Copies: 1}}, serving, nil, true},
		{"copie a zero: l'app è ferma", store.App{Config: store.AppConfig{Copies: 0}}, serving, nil, false},
		{"repo archiviato: l'app resta ferma", store.App{Config: store.AppConfig{Copies: 1}, RepoArchivedAt: &archived}, serving, nil, false},
		{"primo deploy fallito", store.App{Status: "failed", Config: store.AppConfig{Copies: 1}}, serving, nil, false},
		{"versione non in servizio", store.App{Config: store.AppConfig{Copies: 1}}, store.AppVersion{Number: 2, State: "failed"}, nil, false},
		{"versione non letta", store.App{Config: store.AppConfig{Copies: 1}}, store.AppVersion{}, errors.New("db"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldReapply(c.app, c.v, c.verr); got != c.want {
				t.Errorf("shouldReapply = %v, atteso %v", got, c.want)
			}
		})
	}
}

func TestApply_RicreaIlDeploymentDellaVersioneAttiva(t *testing.T) {
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 2, Config: store.AppConfig{Copies: 1}}
	v := store.AppVersion{AppID: app.ID, Number: 2, State: "serving", Image: store.AppImage{Reference: "registry.gitstack.test/alice/web:v2", Source: "registry"}}
	kube := fake.NewSimpleClientset()
	c := New(nil, kube, nil, Config{})
	if err := c.apply(context.Background(), app, v); err != nil {
		t.Fatal(err)
	}
	d, err := kube.AppsV1().Deployments(Namespace(app.ID)).Get(context.Background(), WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("il Deployment deve esserci dopo la riapplicazione: %v", err)
	}
	if got := d.Spec.Template.Spec.Containers[0].Image; got != v.Image.Reference {
		t.Errorf("immagine = %q, attesa %q", got, v.Image.Reference)
	}
}
