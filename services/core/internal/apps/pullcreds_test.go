package apps

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/apppull"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func TestApply_PullSecretDelRegistryDelRepo(t *testing.T) {
	ctx := context.Background()
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 1, Config: store.AppConfig{Copies: 1}}
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	keys := []string{"chiave-gateway"}
	v := store.AppVersion{AppID: app.ID, Number: 1, State: "serving",
		Image: store.AppImage{Reference: "172.27.25.76/admin/voltest/e2eimg:v1", Source: "registry"}}
	kube := fake.NewClientset()
	c := New(nil, kube, nil, Config{GateImage: testGateImage, PullCredentials: PullCredentialsFor(keys, func() time.Time { return now }), Now: func() time.Time { return now }})
	if err := c.apply(ctx, app, v); err != nil {
		t.Fatal(err)
	}
	ns := Namespace(app.ID)
	d, err := kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if refs := d.Spec.Template.Spec.ImagePullSecrets; len(refs) != 1 || refs[0].Name != pullSecretName {
		t.Fatalf("imagePullSecrets = %+v", refs)
	}
	sec, err := kube.CoreV1().Secrets(ns).Get(ctx, pullSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("il Secret deve stare nel namespace dell'app: %v", err)
	}
	if sec.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("tipo = %q", sec.Type)
	}
	var cfg struct {
		Auths map[string]struct{ Username, Password string } `json:"auths"`
	}
	if err := json.Unmarshal(sec.Data[corev1.DockerConfigJsonKey], &cfg); err != nil {
		t.Fatal(err)
	}
	a, ok := cfg.Auths["172.27.25.76"]
	if !ok || a.Username != apppull.Username {
		t.Fatalf("auths = %+v (il server deve essere l'host dell'immagine)", cfg.Auths)
	}
	// La password è un token di pull di quell'app, valido con la chiave del gateway.
	if id, err := apppull.Verify(keys, a.Password, now); err != nil || id != app.ID {
		t.Fatalf("token del pull secret: %v %v", id, err)
	}
}

func TestApply_ImmagineEsternaSenzaPullSecret(t *testing.T) {
	ctx := context.Background()
	app := store.App{ID: uuid.New(), Name: "web", ActiveVersion: 1, Config: store.AppConfig{Copies: 1}}
	v := store.AppVersion{AppID: app.ID, Number: 1, State: "serving", Image: store.AppImage{Reference: "docker.io/library/nginx:1", Source: "external"}}
	kube := fake.NewClientset()
	c := New(nil, kube, nil, Config{GateImage: testGateImage, PullCredentials: PullCredentialsFor([]string{"k"}, time.Now)})
	if err := c.apply(ctx, app, v); err != nil {
		t.Fatal(err)
	}
	d, _ := kube.AppsV1().Deployments(Namespace(app.ID)).Get(ctx, WorkloadName, metav1.GetOptions{})
	if len(d.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Fatal("un'immagine esterna non deve ricevere le credenziali del registry")
	}
	if _, err := kube.CoreV1().Secrets(Namespace(app.ID)).Get(ctx, pullSecretName, metav1.GetOptions{}); err == nil {
		t.Fatal("pull secret creato per un'immagine esterna")
	}
}

func TestPullCredentialsFor_SenzaChiaveNessunaCredenziale(t *testing.T) {
	cred, err := PullCredentialsFor(nil, time.Now)(context.Background(), store.App{ID: uuid.New()})
	if err != nil || cred != nil {
		t.Fatalf("cred=%v err=%v", cred, err)
	}
}

func TestImageHost(t *testing.T) {
	for ref, want := range map[string]string{
		"172.27.25.76/admin/voltest/e2eimg:v1": "172.27.25.76",
		"git.example.com:8443/a/b:1":           "git.example.com:8443",
		"localhost/a:1":                        "localhost",
		"nginx:1":                              "",
		"library/nginx:1":                      "",
	} {
		if got := imageHost(ref); got != want {
			t.Errorf("imageHost(%q) = %q, atteso %q", ref, got, want)
		}
	}
}
