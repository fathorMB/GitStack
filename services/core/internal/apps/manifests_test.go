package apps

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

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
	app := store.App{ID: uuid.New(), RepoID: uuid.New(), Name: "web", AddressLabel: &label, Access: "none", Config: cfg}
	ver := store.AppVersion{AppID: app.ID, Number: 7, Config: cfg, Image: store.AppImage{Reference: "registry.example.com/alice/handbook:sha-1", Digest: &digest, Source: "registry"}}
	return app, ver
}

func TestBuildDeployment_AggiornamentoSenzaInterruzioneESalute(t *testing.T) {
	app, ver := testApp()
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 2, ProgressDeadlineSeconds: 90, PullSecret: "registry-pull", AppSecret: "app-secrets", RestartedAt: "ieri"})
	if d.Spec.Template.Spec.PriorityClassName != "gitstack-apps" {
		t.Fatalf("priorityClassName: %q", d.Spec.Template.Spec.PriorityClassName)
	}
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

func TestBuildDeployment_PassaProxyN9ESovrascriveQuelloDellApp(t *testing.T) {
	app, ver := testApp()
	ver.Config.Variables["HTTPS_PROXY"] = "http://non-valido:3128"
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 1, ProxyEnv: ProxyEnvFrom(func(k string) string {
		return map[string]string{"HTTPS_PROXY": "http://proxy:3128", "NO_PROXY": ".svc"}[k]
	})})
	got := map[string]string{}
	for _, v := range d.Spec.Template.Spec.Containers[0].Env {
		got[v.Name] = v.Value
	}
	if got["HTTPS_PROXY"] != "http://proxy:3128" || got["NO_PROXY"] != ".svc" {
		t.Fatalf("proxy N9: %v", got)
	}
}

func TestBuildNetworkPolicy_IsolaAppEConsenteSoloStessoRepo(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app)
	if p.Name != "app-isolation" || p.Namespace != Namespace(app.ID) || p.Labels[LabelRepoID] != app.RepoID.String() {
		t.Fatalf("metadati policy: %+v", p.ObjectMeta)
	}
	if len(p.Spec.PolicyTypes) != 2 || len(p.Spec.Ingress) != 2 || len(p.Spec.Egress) != 1 {
		t.Fatalf("serve deny ingress+egress con una sola apertura: %+v", p.Spec)
	}
	for _, peers := range [][]networkingv1.NetworkPolicyPeer{p.Spec.Ingress[0].From, p.Spec.Egress[0].To} {
		if len(peers) != 1 || peers[0].NamespaceSelector == nil || peers[0].PodSelector == nil ||
			peers[0].NamespaceSelector.MatchLabels[LabelRepoID] != app.RepoID.String() ||
			peers[0].PodSelector.MatchLabels[LabelManaged] != "true" {
			t.Fatalf("peer stesso repo: %+v", peers)
		}
	}
	traefik := p.Spec.Ingress[1]
	if traefik.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" || traefik.From[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "traefik" || traefik.Ports[0].Port.String() != "http" {
		t.Fatalf("ingresso Traefik: %+v", traefik)
	}
}

func TestBuildNetworkPolicy_ProxyIPApriSoloPortaProxy(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{ProxyEnv: map[string]string{"HTTPS_PROXY": "http://10.0.0.5:3128"}})
	if len(p.Spec.Egress) != 2 {
		t.Fatalf("egress senza DNS per IP: %+v", p.Spec.Egress)
	}
	r := p.Spec.Egress[1]
	if r.To[0].IPBlock == nil || r.To[0].IPBlock.CIDR != "10.0.0.5/32" || r.Ports[0].Port.IntValue() != 3128 {
		t.Fatalf("proxy: %+v", r)
	}
}

func TestEnsureNamespace_AggiungeRepoIDMancante(t *testing.T) {
	app, _ := testApp()
	ns := BuildNamespace(app, false)
	delete(ns.Labels, LabelRepoID)
	kube := kubernetesfake.NewClientset(ns)
	c := &Controller{kube: kube}
	if err := c.ensureNamespace(context.Background(), app, false); err != nil {
		t.Fatal(err)
	}
	got, err := kube.CoreV1().Namespaces().Get(context.Background(), ns.Name, metav1.GetOptions{})
	if err != nil || got.Labels[LabelRepoID] != app.RepoID.String() {
		t.Fatalf("repo-id: %v %+v", err, got.Labels)
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
	r := BuildRoute(app, "web-handbook-alice.apps.example.com", true)
	spec := r.Object["spec"].(map[string]any)
	route := spec["routes"].([]any)[0].(map[string]any)
	if route["match"] != "Host(`web-handbook-alice.apps.example.com`)" {
		t.Fatalf("match: %v", route["match"])
	}
	names := func(r map[string]any) []string {
		var out []string
		for _, m := range r["middlewares"].([]any) {
			out = append(out, m.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := names(route); len(got) != 2 || got[0] != MiddlewareStrip || got[1] != MiddlewareAuth {
		t.Fatalf("con login prima si svuotano le intestazioni, poi il forward-auth: %v", got)
	}
	if _, ok := spec["tls"]; !ok || spec["entryPoints"].([]any)[0] != "websecure" {
		t.Fatalf("tls/entryPoints: %v", spec)
	}
	none := BuildRoute(app, "h", false).Object["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	if got := names(none); len(got) != 1 || got[0] != MiddlewareStrip {
		t.Fatalf("senza login resta solo la pulizia delle intestazioni: %v", got)
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
	c.cfg.AuthURL, c.cfg.AuthSecret = "http://gw/_appauth/verify", "s3"
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
