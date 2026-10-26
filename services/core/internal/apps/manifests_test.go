package apps

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
	ver := store.AppVersion{AppID: app.ID, Number: 7, Config: cfg, CreatedAt: time.Date(2026, 10, 26, 9, 30, 15, 0, time.UTC), Image: store.AppImage{Reference: "registry.example.com/alice/handbook:sha-1", Digest: &digest, Source: "registry"}}
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
	if got := envNames(c.Env); got != "A,B,GITSTACK_APP_VERSION,GITSTACK_DEPLOYED_AT,PORT" {
		t.Fatalf("variabili ordinate, di sistema e PORT: %s", got)
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

// envNames elenca i nomi delle variabili del container, nell'ordine in cui le passa il Deployment.
func envNames(env []corev1.EnvVar) string {
	names := make([]string, 0, len(env))
	for _, e := range env {
		names = append(names, e.Name)
	}
	return strings.Join(names, ",")
}

func envValues(env []corev1.EnvVar) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}

func TestBuildDeployment_VariabiliDiSistema(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		digest  bool
		vers    map[string]string
		version string
	}{
		{name: "tag e digest: APP_VERSION è il tag", ref: "registry.example.com/alice/handbook:sha-1", digest: true, version: "sha-1"},
		{name: "solo digest nel riferimento: APP_VERSION assente", ref: "registry.example.com/alice/handbook@sha256:" + strings.Repeat("b", 64)},
		{name: "porta del registry senza tag: APP_VERSION assente", ref: "localhost:5000/app"},
		{name: "tag con digest nel riferimento: il tag prima del digest", ref: "registry.example.com/alice/handbook:v2@sha256:" + strings.Repeat("c", 64), version: "v2"},
		{name: "una variabile dell'app non sovrascrive quella di sistema", ref: "registry.example.com/alice/handbook:sha-1", digest: true, vers: map[string]string{"GITSTACK_APP_VERSION": "finta"}, version: "sha-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, ver := testApp()
			ver.Image.Reference = tc.ref
			if !tc.digest {
				ver.Image.Digest = nil
			}
			for k, v := range tc.vers {
				ver.Config.Variables[k] = v
			}
			ver.CreatedAt = time.Date(2026, 10, 26, 9, 30, 15, 0, time.FixedZone("CEST", 2*3600))
			env := envValues(BuildDeployment(app, ver, BuildOptions{Replicas: 1}).Spec.Template.Spec.Containers[0].Env)
			if got, ok := env[EnvAppVersion]; tc.version == "" && ok || tc.version != "" && got != tc.version {
				t.Fatalf("GITSTACK_APP_VERSION = %q (presente %v), atteso %q", got, ok, tc.version)
			}
			if got := env[EnvDeployedAt]; got != "2026-10-26T07:30:15Z" {
				t.Fatalf("GITSTACK_DEPLOYED_AT = %q, atteso UTC RFC 3339", got)
			}
			if _, ok := env["GITSTACK_COMMIT_SHA"]; ok {
				t.Fatal("GITSTACK_COMMIT_SHA senza etichetta revision non deve esserci (GIT-386)")
			}
			if env["PORT"] != "8080" {
				t.Fatalf("PORT = %q", env["PORT"])
			}
		})
	}
}

func TestIsReservedEnvName(t *testing.T) {
	cases := map[string]bool{
		"GITSTACK_X":           true,
		"GITSTACK_APP_VERSION": true,
		"gitstack_x":           true,
		"GitStack_Token":       true,
		"GITSTACK":             false,
		"MY_GITSTACK_X":        false,
		"DATABASE_URL":         false,
		"":                     false,
	}
	for name, want := range cases {
		if got := IsReservedEnvName(name); got != want {
			t.Errorf("IsReservedEnvName(%q) = %v, atteso %v", name, got, want)
		}
	}
}

func TestBuildNetworkPolicy_IsolaAppEConsenteSoloStessoRepo(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app)
	if p.Name != "app-isolation" || p.Namespace != Namespace(app.ID) || p.Labels[LabelRepoID] != app.RepoID.String() {
		t.Fatalf("metadati policy: %+v", p.ObjectMeta)
	}
	if len(p.Spec.PolicyTypes) != 2 || len(p.Spec.Ingress) != 2 || len(p.Spec.Egress) != 3 {
		t.Fatalf("serve deny ingress+egress con stesso repo e solo DNS: %+v", p.Spec)
	}
	for _, peers := range [][]networkingv1.NetworkPolicyPeer{p.Spec.Ingress[0].From, p.Spec.Egress[0].To} {
		if len(peers) != 1 || peers[0].NamespaceSelector == nil || peers[0].PodSelector == nil ||
			peers[0].NamespaceSelector.MatchLabels[LabelRepoID] != app.RepoID.String() ||
			peers[0].PodSelector.MatchLabels[LabelManaged] != "true" {
			t.Fatalf("peer stesso repo: %+v", peers)
		}
	}
	dns := p.Spec.Egress[1]
	if len(dns.To) != 1 || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" || dns.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" || len(dns.Ports) != 2 || dns.Ports[0].Port.IntValue() != 53 || dns.Ports[1].Port.IntValue() != 53 {
		t.Fatalf("DNS di cluster sempre aperto, e solo quello (H15): %+v", dns)
	}
	traefik := p.Spec.Ingress[1]
	if traefik.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" || traefik.From[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "traefik" || traefik.Ports[0].Port.String() != "http" {
		t.Fatalf("ingresso Traefik: %+v", traefik)
	}
}

func TestBuildNetworkPolicy_ProxyIPApriSoloPortaProxy(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{ProxyEnv: map[string]string{"HTTPS_PROXY": "http://10.0.0.5:3128"}})
	if len(p.Spec.Egress) != 4 {
		t.Fatalf("egress: stesso repo, DNS e proxy: %+v", p.Spec.Egress)
	}
	r := p.Spec.Egress[2]
	if r.To[0].IPBlock == nil || r.To[0].IPBlock.CIDR != "10.0.0.5/32" || r.Ports[0].Port.IntValue() != 3128 {
		t.Fatalf("proxy: %+v", r)
	}
}

func TestBuildNetworkPolicy_ProxyMinuscoloApriSoloPortaProxy(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{ProxyEnv: map[string]string{"http_proxy": "http://10.0.0.6:3128"}})
	if len(p.Spec.Egress) != 4 {
		t.Fatalf("egress proxy minuscolo: %+v", p.Spec.Egress)
	}
	r := p.Spec.Egress[2]
	if r.To[0].IPBlock == nil || r.To[0].IPBlock.CIDR != "10.0.0.6/32" || r.Ports[0].Port.IntValue() != 3128 {
		t.Fatalf("proxy minuscolo: %+v", r)
	}
}

func TestBuildNetworkPolicy_ProxyNomeApreDNSEIP(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{
		ProxyEnv: map[string]string{"HTTPS_PROXY": "http://proxy.lan:3128"},
		ProxyIPs: map[string][]net.IP{"proxy.lan": {net.ParseIP("10.0.0.7")}},
	})
	if len(p.Spec.Egress) != 4 {
		t.Fatalf("egress proxy con DNS: %+v", p.Spec.Egress)
	}
	dns, proxy := p.Spec.Egress[1], p.Spec.Egress[2]
	if len(dns.Ports) != 2 || dns.Ports[0].Port.IntValue() != 53 || dns.Ports[1].Port.IntValue() != 53 || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" {
		t.Fatalf("DNS proxy: %+v", dns)
	}
	if proxy.To[0].IPBlock == nil || proxy.To[0].IPBlock.CIDR != "10.0.0.7/32" || proxy.Ports[0].Port.IntValue() != 3128 {
		t.Fatalf("proxy risolto: %+v", proxy)
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

func TestBuildService_PortaDichiarataOltreLa80(t *testing.T) {
	app, _ := testApp() // porta 8080
	ports := BuildService(app, app.Config).Spec.Ports
	if len(ports) != 2 || ports[0].Name != "http" || ports[0].Port != 80 || ports[0].TargetPort.String() != "http" {
		t.Fatalf("la 80 resta per ingress e forward-auth: %+v", ports)
	}
	if ports[1].Name != DeclaredPortName || ports[1].Port != 8080 || ports[1].TargetPort.IntValue() != 8080 {
		t.Fatalf("porta dichiarata: %+v", ports[1])
	}
	p80 := 80
	cfg := app.Config
	cfg.Port = &p80
	if ports := BuildService(app, cfg).Spec.Ports; len(ports) != 1 || ports[0].Port != 80 {
		t.Fatalf("dichiarata = 80: una porta sola: %+v", ports)
	}
}

func aliasApp(repo uuid.UUID, name string, port bool) store.App {
	a := store.App{ID: uuid.New(), RepoID: repo, Name: name}
	if port {
		p := 5432
		a.Config.Port = &p
	}
	return a
}

func TestBuildSiblingAliases_SoloStessoRepoENomiValidi(t *testing.T) {
	repo := uuid.New()
	site, api := aliasApp(repo, "site", true), aliasApp(repo, "api", true)
	other := aliasApp(uuid.New(), "api", true) // stesso nome, altro repo
	got, skipped := BuildSiblingAliases(site, []store.App{site, api, other, aliasApp(repo, "app", true), aliasApp(repo, "9db", true), aliasApp(repo, "worker", false)})
	if len(got) != 1 {
		t.Fatalf("solo api: %+v", got)
	}
	a := got[0]
	if a.Name != "api" || a.Namespace != Namespace(site.ID) || a.Spec.Type != corev1.ServiceTypeExternalName ||
		a.Spec.ExternalName != "app."+Namespace(api.ID)+".svc.cluster.local" || a.Labels[LabelSiblingAlias] != "true" {
		t.Fatalf("alias: %+v", a)
	}
	if a.Spec.ExternalName == "app."+Namespace(other.ID)+".svc.cluster.local" {
		t.Fatal("l'alias punta all'app di un altro repo")
	}
	if len(skipped) != 2 || skipped[0].Name != "app" || skipped[1].Name != "9db" {
		t.Fatalf("saltati: %+v", skipped)
	}
}

func svcNames(t *testing.T, kube kubernetes.Interface, ns string) map[string]corev1.Service {
	t.Helper()
	l, err := kube.CoreV1().Services(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]corev1.Service{}
	for _, s := range l.Items {
		out[s.Name] = s
	}
	return out
}

func TestReconcileSiblingAliases_CreaRinominaEliminaESaltaLeCollisioni(t *testing.T) {
	repo := uuid.New()
	site, api := aliasApp(repo, "site", true), aliasApp(repo, "api", true)
	kube := kubernetesfake.NewClientset()
	c := New(nil, kube, nil, Config{})
	ctx := context.Background()
	ns := Namespace(site.ID)
	// un Service dell'app stessa, e uno estraneo con il nome di un'altra sorella
	if _, err := kube.CoreV1().Services(ns).Create(ctx, BuildService(site, site.Config), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	foreign := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: ns}}
	if _, err := kube.CoreV1().Services(ns).Create(ctx, foreign, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cache := aliasApp(repo, "cache", true)
	skipped, err := c.reconcileSiblingAliases(ctx, site, []store.App{site, api, cache})
	if err != nil {
		t.Fatal(err)
	}
	got := svcNames(t, kube, ns)
	if got["api"].Spec.ExternalName != "app."+Namespace(api.ID)+".svc.cluster.local" {
		t.Fatalf("alias api mancante: %+v", got)
	}
	if got["cache"].Spec.Type == corev1.ServiceTypeExternalName || len(skipped) != 1 || skipped[0].Name != "cache" {
		t.Fatalf("il Service estraneo non si tocca: %+v %+v", got["cache"], skipped)
	}
	if _, ok := got["app"]; !ok {
		t.Fatal("il Service dell'app non si tocca")
	}
	// idempotente
	if _, err := c.reconcileSiblingAliases(ctx, site, []store.App{site, api, cache}); err != nil {
		t.Fatal(err)
	}
	// api rinominata in backend: il vecchio nome sparisce, il nuovo compare
	api.Name = "backend"
	if _, err := c.reconcileSiblingAliases(ctx, site, []store.App{site, api}); err != nil {
		t.Fatal(err)
	}
	got = svcNames(t, kube, ns)
	if _, ok := got["api"]; ok || got["backend"].Spec.ExternalName == "" {
		t.Fatalf("rinomina: %+v", got)
	}
	// api eliminata: restano solo il Service dell'app e quello estraneo
	if _, err := c.reconcileSiblingAliases(ctx, site, []store.App{site}); err != nil {
		t.Fatal(err)
	}
	got = svcNames(t, kube, ns)
	if len(got) != 2 || got["app"].Name == "" || got["cache"].Name == "" {
		t.Fatalf("eliminazione: %+v", got)
	}
}

func TestBuildNetworkPolicy_ProxyIPv6Usa128EIPv4Usa32(t *testing.T) {
	app, _ := testApp()
	for url, want := range map[string]string{
		"http://[fd00::5]:3128": "fd00::5/128",
		"http://10.0.0.5:3128":  "10.0.0.5/32",
	} {
		p := BuildNetworkPolicy(app, NetworkPolicyOptions{ProxyEnv: map[string]string{"HTTPS_PROXY": url}})
		r := p.Spec.Egress[2]
		if r.To[0].IPBlock == nil || r.To[0].IPBlock.CIDR != want {
			t.Errorf("%s: CIDR %+v, voluto %s", url, r.To[0].IPBlock, want)
		}
	}
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{
		ProxyEnv: map[string]string{"HTTPS_PROXY": "http://proxy.lan:3128"},
		ProxyIPs: map[string][]net.IP{"proxy.lan": {net.ParseIP("fd00::7"), net.ParseIP("10.0.0.7")}},
	})
	if got := p.Spec.Egress[2].To[0].IPBlock.CIDR; got != "fd00::7/128" {
		t.Errorf("proxy con nome IPv6: %s", got)
	}
	if got := p.Spec.Egress[3].To[0].IPBlock.CIDR; got != "10.0.0.7/32" {
		t.Errorf("proxy con nome IPv4: %s", got)
	}
}

func TestBuildNetworkPolicy_InternetApertoConBarriere(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{ClusterCIDRs: []string{"10.50.0.0/16"}, NodeAddresses: []string{"192.168.1.10"}})
	r := p.Spec.Egress[len(p.Spec.Egress)-1]
	if len(r.Ports) != 0 || len(r.To) != 2 || r.To[0].IPBlock.CIDR != "0.0.0.0/0" || r.To[1].IPBlock.CIDR != "::/0" {
		t.Fatalf("regola internet: %+v", r)
	}
	has := func(l []string, s string) bool {
		for _, x := range l {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "10.50.0.0/16", "192.168.1.10/32"} {
		if !has(r.To[0].IPBlock.Except, s) {
			t.Errorf("manca %s tra le eccezioni IPv4", s)
		}
	}
	for _, s := range []string{"fc00::/7", "fe80::/10", "::/96"} {
		if !has(r.To[1].IPBlock.Except, s) {
			t.Errorf("manca %s tra le eccezioni IPv6", s)
		}
	}
	// Una sola policy, e nessun'altra regola apre indirizzi fuori dal proxy.
	if p.Name != "app-isolation" {
		t.Fatal(p.Name)
	}
}
