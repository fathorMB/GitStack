//go:build integration

package apps

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// GIT-373 (H8): il reconciler usa soltanto i valori correnti e lascia il Deployment privo dei
// valori dei segreti. Il client Kubernetes finto rende questa prova parte dei test integration
// normali, senza richiedere k3d.
func TestController_SegretiKubernetesCorrentiERimossi(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	repoID, ownerID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/secrets')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'secrets')`, repoID, ownerID); err != nil {
		t.Fatal(err)
	}
	port := 8080
	app, v1, err := st.CreateApp(ctx, store.NewApp{
		RepoID: repoID,
		Owner:  "alice",
		Name:   "web",
		Image:  store.AppImage{Reference: "registry.example/alice/secrets:web", Source: "registry"},
		Config: store.AppConfig{
			Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20},
		},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	keys, err := webhooks.NewKeyring("k1", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "")
	if err != nil {
		t.Fatal(err)
	}
	_, v2, err := st.PutAppSecret(ctx, keys, app.ID, "TOKEN", "first-secret-value", nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	kube := kubernetesfake.NewSimpleClientset()
	ctl := New(st, kube, fakeDynamic(), Config{Secrets: keys})
	ns := Namespace(app.ID)

	apply := func(version store.AppVersion) {
		t.Helper()
		if err := ctl.apply(ctx, app, version); err != nil {
			t.Fatal(err)
		}
	}
	secret := func() string {
		t.Helper()
		s, err := kube.CoreV1().Secrets(ns).Get(ctx, appSecretName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return string(s.Data["TOKEN"])
	}

	apply(v2)
	if got := secret(); got != "first-secret-value" {
		t.Fatalf("Secret della versione con TOKEN: %q", got)
	}
	d, err := kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	container := d.Spec.Template.Spec.Containers[0]
	if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef == nil || container.EnvFrom[0].SecretRef.Name != appSecretName {
		t.Fatalf("Deployment senza envFrom del Secret: %+v", container.EnvFrom)
	}
	if strings.Contains(fmt.Sprintf("%+v", d.Spec), "first-secret-value") || strings.Contains(fmt.Sprintf("%+v", d.Spec), "rotated-secret-value") {
		t.Fatal("il Deployment contiene un valore di segreto in chiaro")
	}

	_, v3, err := st.PutAppSecret(ctx, keys, app.ID, "TOKEN", "rotated-secret-value", nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	apply(v3)
	if got := secret(); got != "rotated-secret-value" {
		t.Fatalf("Secret dopo rotazione: %q", got)
	}
	// La versione 2 conserva il nome TOKEN, ma rileggendo il valore dopo la rotazione ottiene quello corrente.
	apply(v2)
	if got := secret(); got != "rotated-secret-value" {
		t.Fatalf("rollback della versione con TOKEN ha ripristinato un valore storico: %q", got)
	}

	apply(v1)
	if _, err := kube.CoreV1().Secrets(ns).Get(ctx, appSecretName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Secret dopo una versione senza segreti: %v", err)
	}

	apply(v2)
	if err := st.DeleteApp(ctx, repoID, app.Name); err != nil {
		t.Fatal(err)
	}
	ctl.Collect(ctx)
	if _, err := kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("namespace dell'app eliminata: %v", err)
	}
}
