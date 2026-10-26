//go:build integration

package httpserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-351 (H4): dopo `gitstack restore` su un cluster pulito il database ha l'app con la sua
// versione attiva in servizio, ma il Deployment non esiste. Il controller la riapplica: l'app
// riparte con la sua versione e la sua configurazione, senza deploy a mano.
//
// Il cluster è finto (client-go fake) e il registry non c'è: si prova la parte di core (database
// → Deployment), non il pull dell'immagine né la salute reale, che restano al passo e2e sulla VM.
func TestApps_DopoIlRestoreLaVersioneAttivaRiparte(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")

	ctx := context.Background()
	st := store.New(e.pool)
	ids, err := st.AppIDs(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("attesa una sola app: %v, %v", ids, err)
	}
	// Stato che il restore riporta: versione 1 in servizio, nessun Deployment nel cluster nuovo.
	if err := st.FinishAppDeploy(ctx, ids[0], 1, "serving", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientset()
	ctl := apps.New(st, kube, nil, apps.Config{AppsDomain: "apps.example.com", ProgressTimeout: 75 * time.Second})

	ctl.Sync(ctx)

	d, err := kube.AppsV1().Deployments(apps.Namespace(ids[0])).Get(ctx, apps.WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dopo la riapplicazione il Deployment deve esserci: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img == "" {
		t.Errorf("il Deployment non ha un'immagine")
	}
}
