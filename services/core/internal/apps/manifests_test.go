package apps

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func testApp() (store.App, store.AppVersion) {
	port := 8080
	label := "web-handbook-alice"
	digest := "sha256:" + strings.Repeat("a", 64)
	cfg := store.AppConfig{
		Port: &port, HealthCheck: &store.AppHealthCheck{Path: "/healthz", IntervalSeconds: 3},
		Variables:  map[string]string{"B": "2", "A": "1"},
		Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
		Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}, Copies: 2,
	}
	app := store.App{ID: uuid.New(), Name: "web", AddressLabel: &label, Access: "none", Config: cfg}
	ver := store.AppVersion{AppID: app.ID, Number: 7, Config: cfg, Image: store.AppImage{Reference: "registry.example.com/alice/handbook:sha-1", Digest: &digest, Source: "registry"}}
	return app, ver
}

func TestBuildDeployment_AggiornamentoSenzaInterruzioneESalute(t *testing.T) {
	app, ver := testApp()
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 2, ProgressDeadlineSeconds: 90, PullSecret: "registry-pull", AppSecret: "app-secrets", RestartedAt: "ieri"})
	if d.Namespace != "gitstack-app-"+app.ID.String() || d.Labels[LabelManaged] != "true" {
		t.Fatalf("namespace/label: %s %v", d.Namespace, d.Labels)
	}
	ru := d.Spec.Strategy.RollingUpdate
	if ru.MaxUnavailable.IntValue() != 0 || ru.MaxSurge.IntValue() != 1 {
		t.Fatalf("strategia: %+v", ru)
	}
	if *d.Spec.Replicas != 2 || *d.Spec.ProgressDeadlineSeconds != 90 {
		t.Fatalf("repliche/deadline: %d %d", *d.Spec.Replicas, *d.Spec.ProgressDeadlineSeconds)
	}
	tpl := d.Spec.Template
	if tpl.Annotations[AnnotationVersion] != "7" || tpl.Annotations[AnnotationRestartedAt] != "ieri" {
		t.Fatalf("annotazioni: %v", tpl.Annotations)
	}
	c := tpl.Spec.Containers[0]
	if c.Image != "registry.example.com/alice/handbook@"+*ver.Image.Digest {
		t.Fatalf("l'immagine va fissata per digest: %s", c.Image)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/healthz" || c.ReadinessProbe.PeriodSeconds != 3 {
		t.Fatalf("readiness: %+v", c.ReadinessProbe)
	}
	if c.LivenessProbe == nil || c.Lifecycle == nil || c.Lifecycle.PreStop.Sleep == nil {
		t.Fatalf("liveness/preStop: %+v %+v", c.LivenessProbe, c.Lifecycle)
	}
	if got := c.Env[0].Name + c.Env[1].Name + c.Env[2].Name; got != "ABPORT" {
		t.Fatalf("variabili ordinate e PORT: %s", got)
	}
	if len(tpl.Spec.ImagePullSecrets) != 1 || tpl.Spec.ImagePullSecrets[0].Name != "registry-pull" {
		t.Fatalf("pull secret: %+v", tpl.Spec.ImagePullSecrets)
	}
	if len(c.EnvFrom) != 1 || c.EnvFrom[0].SecretRef == nil || c.EnvFrom[0].SecretRef.Name != "app-secrets" {
		t.Fatalf("secret dell'app: %+v", c.EnvFrom)
	}
	if *tpl.Spec.AutomountServiceAccountToken || *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.RunAsNonRoot != nil {
		t.Fatalf("sicurezza: %+v", c.SecurityContext)
	}
	if c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("capability: %+v", c.SecurityContext.Capabilities)
	}
}

func TestBuildDeployment_RestrictedNonGiraComeRoot(t *testing.T) {
	app, ver := testApp()
	ver.Config.Restricted = true
	c := BuildDeployment(app, ver, BuildOptions{Replicas: 1}).Spec.Template.Spec.Containers[0]
	if c.SecurityContext.RunAsNonRoot == nil || !*c.SecurityContext.RunAsNonRoot {
		t.Fatalf("restricted deve chiedere runAsNonRoot: %+v", c.SecurityContext)
	}
}

func TestBuildDeployment_SenzaPortaNiente_Sonde(t *testing.T) {
	app, ver := testApp()
	ver.Config.Port, ver.Config.HealthCheck = nil, nil
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 1})
	c := d.Spec.Template.Spec.Containers[0]
	if c.ReadinessProbe != nil || len(c.Ports) != 0 || d.Spec.MinReadySeconds == 0 {
		t.Fatalf("senza porta: sonde %+v porte %v minReady %d", c.ReadinessProbe, c.Ports, d.Spec.MinReadySeconds)
	}
	if BuildService(app, ver.Config) != nil {
		t.Fatal("un'app senza porta non ha Service")
	}
}

func TestBuildDeployment_ControlloTcpSenzaHealthCheck(t *testing.T) {
	app, ver := testApp()
	ver.Config.HealthCheck = nil
	c := BuildDeployment(app, ver, BuildOptions{Replicas: 1}).Spec.Template.Spec.Containers[0]
	if c.ReadinessProbe.TCPSocket == nil || c.ReadinessProbe.TCPSocket.Port.IntValue() != 8080 {
		t.Fatalf("readiness TCP: %+v", c.ReadinessProbe)
	}
}

func TestBuildServiceEdRotta(t *testing.T) {
	app, ver := testApp()
	svc := BuildService(app, ver.Config)
	if svc.Spec.Ports[0].Port != 80 || svc.Spec.Ports[0].TargetPort.String() != "http" {
		t.Fatalf("service: %+v", svc.Spec.Ports)
	}
	r := BuildRoute(app, "web-handbook-alice.apps.example.com", "gitstack/forward-auth")
	spec := r.Object["spec"].(map[string]any)
	route := spec["routes"].([]any)[0].(map[string]any)
	if route["match"] != "Host(`web-handbook-alice.apps.example.com`)" {
		t.Fatalf("match: %v", route["match"])
	}
	mw := route["middlewares"].([]any)[0].(map[string]any)
	if mw["name"] != "forward-auth" || mw["namespace"] != "gitstack" {
		t.Fatalf("middleware: %v", mw)
	}
	if _, ok := spec["tls"]; !ok || spec["entryPoints"].([]any)[0] != "websecure" {
		t.Fatalf("tls/entryPoints: %v", spec)
	}
	if _, ok := BuildRoute(app, "h", "").Object["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)["middlewares"]; ok {
		t.Fatal("senza middleware non va dichiarato")
	}
}

func TestRouteHost_LoginSenzaMiddlewareNonHaRotta(t *testing.T) {
	app, ver := testApp()
	c := &Controller{cfg: Config{AppsDomain: "apps.example.com"}, dyn: nil}
	if c.routeHost(app, ver.Config) != "" {
		t.Fatal("senza client dinamico niente rotta")
	}
	c.dyn = fakeDynamic()
	app.Access = "login"
	if h := c.routeHost(app, ver.Config); h != "" {
		t.Fatalf("login senza forward-auth: nessuna rotta (mai esporre senza login), trovato %q", h)
	}
	c.cfg.AuthMiddleware = "fa"
	if h := c.routeHost(app, ver.Config); h != "web-handbook-alice.apps.example.com" {
		t.Fatalf("host: %q", h)
	}
}

func TestContainerFailure(t *testing.T) {
	cases := []struct {
		name, reason string
		restarts     int32
		exit         int32
		want         string
	}{
		{"pull", "ImagePullBackOff", 0, 0, "ImagePullBackOff"},
		{"crash", "CrashLoopBackOff", 3, 1, "continuazione (codice 1"},
		{"in avvio", "ContainerCreating", 0, 0, ""},
	}
	for _, c := range cases {
		cs := corev1ContainerStatus(c.reason, c.restarts, c.exit)
		if got := containerFailure(cs); (c.want == "" && got != "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, atteso %q", c.name, got, c.want)
		}
	}
}

func TestRolloutComplete(t *testing.T) {
	two := int32(2)
	if !rolloutComplete(&two, 2, 2, 2, 2) || rolloutComplete(&two, 3, 2, 2, 2) || rolloutComplete(&two, 2, 1, 2, 2) || rolloutComplete(&two, 2, 2, 1, 1) {
		t.Fatal("rolloutComplete")
	}
	zero := int32(0)
	if !rolloutComplete(&zero, 0, 0, 0, 0) {
		t.Fatal("zero copie è a regime")
	}
}

func TestDesiredReplicas_RepoArchiviatoFermaLApp(t *testing.T) {
	app, _ := testApp()
	if desiredReplicas(app) != 2 {
		t.Fatal("copie")
	}
	now := time.Now()
	app.RepoArchivedAt = &now
	if desiredReplicas(app) != 0 {
		t.Fatal("repo archiviato: zero copie")
	}
}
