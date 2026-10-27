package apps

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAuthAddress_PortaAppRepoEFirma(t *testing.T) {
	app, repo := uuid.New(), uuid.New()
	addr := AuthAddress("http://gw:8080/_appauth/verify", "segreto", app, repo)
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("app") != app.String() || q.Get("repo") != repo.String() {
		t.Fatalf("app/repo: %v", q)
	}
	if q.Get("sig") != AuthSignature("segreto", app, repo) {
		t.Fatal("la firma dell'indirizzo non torna")
	}
	if AuthSignature("altro", app, repo) == q.Get("sig") || AuthSignature("segreto", app, uuid.New()) == q.Get("sig") {
		t.Fatal("la firma deve dipendere da segreto e repo")
	}
}

func TestBuildMiddlewares_PuliscePrimaEPoiChiedeAlGateway(t *testing.T) {
	app, repo := uuid.New(), uuid.New()
	strip := BuildStripMiddleware(app)
	hdr, _, _ := nestedMap(strip.Object, "spec", "headers", "customRequestHeaders")
	for _, h := range StripHeaders {
		v, ok := hdr[h]
		if !ok || v != "" {
			t.Errorf("%s va svuotata dalla richiesta del client: %v", h, hdr[h])
		}
	}
	auth := BuildAuthMiddleware(app, repo, "http://gw/_appauth/verify", "s")
	fa, _, _ := nestedMap(auth.Object, "spec", "forwardAuth")
	if fa["trustForwardHeader"] != false {
		t.Fatal("trustForwardHeader deve restare falso")
	}
	var resp []string
	for _, h := range fa["authResponseHeaders"].([]any) {
		resp = append(resp, h.(string))
	}
	// Il cookie e Authorization tornano dal gateway già ripuliti: Traefik li sostituisce a quelli
	// del client, quindi la sessione GitStack non arriva all'app.
	for _, want := range []string{"Cookie", "Authorization", "X-GitStack-User", "X-GitStack-Signature"} {
		if !slices.Contains(resp, want) {
			t.Errorf("authResponseHeaders senza %s: %v", want, resp)
		}
	}
	// Ogni intestazione firmata che il gateway restituisce è anche tra quelle svuotate dal client.
	for _, h := range resp {
		if strings.HasPrefix(h, "X-GitStack-") && !slices.Contains(StripHeaders, h) {
			t.Errorf("%s non è nell'elenco delle intestazioni svuotate", h)
		}
	}
	if !strings.Contains(fa["address"].(string), "sig=") {
		t.Fatalf("indirizzo non firmato: %v", fa["address"])
	}
}

func nestedMap(obj map[string]any, path ...string) (map[string]any, bool, error) {
	cur := obj
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur = next
	}
	return cur, true, nil
}

func TestEnsureRoute_ModoDiAccesso(t *testing.T) {
	ctx := context.Background()
	app, ver := testApp()
	app.RepoID = uuid.New()
	dyn := fakeDynamic()
	c := &Controller{dyn: dyn, cfg: Config{AppsDomain: "apps.example.com", AuthURL: "http://gw/_appauth/verify", AuthSecret: "s"}}
	ns := Namespace(app.ID)
	mwHas := func(name string) bool {
		_, err := dyn.Resource(middlewareGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		return err == nil
	}
	routeMws := func() []string {
		r, err := dyn.Resource(routeGVR).Namespace(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		route := r.Object["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
		var out []string
		for _, m := range route["middlewares"].([]any) {
			out = append(out, m.(map[string]any)["name"].(string))
		}
		return out
	}

	// Il default è login: forward-auth davanti all'app.
	app.Access = "login"
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if !mwHas(MiddlewareAuth) || !mwHas(MiddlewareStrip) || !slices.Equal(routeMws(), []string{MiddlewareStrip, MiddlewareAuth}) {
		t.Fatalf("login: middleware %v", routeMws())
	}
	// Un secondo giro identico non cambia nulla e non fallisce.
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}

	// «Senza login» è una scelta esplicita: il forward-auth sparisce, la pulizia resta.
	app.Access = "none"
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if mwHas(MiddlewareAuth) || !mwHas(MiddlewareStrip) || !slices.Equal(routeMws(), []string{MiddlewareStrip}) {
		t.Fatalf("none: middleware %v", routeMws())
	}

	// Tornare a login rimette il forward-auth.
	app.Access = "login"
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if !mwHas(MiddlewareAuth) || !slices.Equal(routeMws(), []string{MiddlewareStrip, MiddlewareAuth}) {
		t.Fatalf("di nuovo login: %v", routeMws())
	}

	// Senza forward-auth configurato un'app «login» non ha rotta: mai esposta senza il login.
	c.cfg.AuthURL = ""
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(routeGVR).Namespace(ns).Get(ctx, WorkloadName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("la rotta di un'app login senza forward-auth deve essere tolta: %v", err)
	}
}

// Stesso vettore del test del gateway (appauth.TestSigFor_VettoreFisso): la firma dell'indirizzo
// la calcola core e la verifica il gateway, in due moduli senza codice in comune.
func TestAuthSignature_VettoreFisso(t *testing.T) {
	app := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	repo := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	const want = "cbc52710e95d93846b04998b95ed76d37d83fdd09c309a6695c9e44cf17bd0db"
	if got := AuthSignature("s3", app, repo); got != want {
		t.Fatalf("firma dell'indirizzo cambiata: %s", got)
	}
}

// TestEnsureRoute_PubblicaSoloConInterruttore (GIT-456, H1): un'app `public` ha la rotta solo con
// l'interruttore d'istanza acceso; spento la rotta sparisce (il modo resta all'app) e la
// riaccensione la rimette. La rotta pubblica non ha il forward-auth.
func TestEnsureRoute_PubblicaSoloConInterruttore(t *testing.T) {
	ctx := context.Background()
	app, ver := testApp()
	app.RepoID = uuid.New()
	app.Access = "public"
	dyn := fakeDynamic()
	on := false
	c := &Controller{dyn: dyn, cfg: Config{AppsDomain: "apps.example.com", AuthURL: "http://gw/_appauth/verify", AuthSecret: "s"},
		publicOn: func(context.Context) (bool, error) { return on, nil }}
	ns := Namespace(app.ID)
	hasRoute := func() bool {
		_, err := dyn.Resource(routeGVR).Namespace(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		return err == nil
	}
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if hasRoute() {
		t.Fatal("interruttore spento: un'app public non deve avere la rotta")
	}
	on = true
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if !hasRoute() {
		t.Fatal("interruttore acceso: la rotta dell'app public deve esserci")
	}
	if _, err := dyn.Resource(middlewareGVR).Namespace(ns).Get(ctx, MiddlewareAuth, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("un'app public non ha il forward-auth: %v", err)
	}
	on = false
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if hasRoute() {
		t.Fatal("interruttore spento di nuovo: la rotta deve sparire")
	}
	on = true
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if !hasRoute() {
		t.Fatal("la riaccensione deve ripristinare la rotta")
	}
	// Errore di lettura dell'interruttore: niente rotta nuova, errore al chiamante.
	c.publicOn = func(context.Context) (bool, error) { return false, context.DeadlineExceeded }
	if err := c.ensureRoute(ctx, app, ver.Config); err == nil {
		t.Fatal("errore di lettura: ensureRoute deve fallire")
	}
}
