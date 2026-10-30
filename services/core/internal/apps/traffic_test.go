package apps

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// GIT-403 (H12): il testo di Traefik diventa richieste ed errori per app; i contatori diventano differenze.

func TestParseTraefikTraffic_SoloIlNamespaceDellApp(t *testing.T) {
	id, other := uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("22222222-2222-2222-2222-222222222222")
	ns, ons := Namespace(id), Namespace(other)
	text := []byte("# HELP traefik_service_requests_total How many HTTP requests processed on a service.\n" +
		"# TYPE traefik_service_requests_total counter\n" +
		`traefik_service_requests_total{code="200",method="GET",protocol="http",service="` + ns + `-web-80@kubernetescrd"} 40` + "\n" +
		`traefik_service_requests_total{code="404",method="GET",protocol="http",service="` + ns + `-web-80@kubernetescrd"} 3` + "\n" +
		`traefik_service_requests_total{code="500",method="POST",protocol="http",service="` + ns + `-web-80@kubernetescrd"} 2` + "\n" +
		`traefik_service_requests_total{code="503",method="GET",protocol="http",service="` + ns + `-web-80@kubernetescrd"} 1e0` + "\n" +
		`traefik_service_requests_total{code="500",method="GET",protocol="http",service="` + ons + `-web-80@kubernetescrd"} 99` + "\n" +
		`traefik_service_requests_total{code="200",method="GET",protocol="http",service="` + ns + `-web-80@file"} 7` + "\n" +
		`traefik_service_requests_total{code="200",method="GET",protocol="http",service="api@internal"} 5` + "\n" +
		`traefik_router_requests_total{code="500",router="` + ns + `-x@kubernetescrd"} 11` + "\n")
	got, found, err := parseTraefikTraffic(text, ns)
	if err != nil || !found {
		t.Fatalf("lettura: %v %v", found, err)
	}
	if got.Requests != 46 || got.Errors5xx != 3 {
		t.Errorf("richieste %v (volute 46), errori %v (voluti 3)", got.Requests, got.Errors5xx)
	}
	if _, found, _ := parseTraefikTraffic(text, Namespace(uuid.New())); found {
		t.Error("un'app senza serie non ha righe")
	}
}

func TestParseTraefikTraffic_EtichetteConEscapeERigheRotte(t *testing.T) {
	ns := Namespace(uuid.New())
	text := []byte(`traefik_service_requests_total{service="` + ns + `-w\"x-80@kubernetescrd",code="502"} 4` + "\n")
	got, found, err := parseTraefikTraffic(text, ns)
	if err != nil || !found || got.Requests != 4 || got.Errors5xx != 4 {
		t.Fatalf("escape: %+v %v %v", got, found, err)
	}
	for _, bad := range []string{
		`traefik_service_requests_total{service="` + ns + `-w-80@kubernetescrd",code="200"}`,
		`traefik_service_requests_total{service="` + ns + `-w-80@kubernetescrd",code="200"} tre`,
		`traefik_service_requests_total{service="` + ns + `-w-80@kubernetescrd",code="200"} -1`,
		`traefik_service_requests_total{service="` + ns + `-w-80@kubernetescrd`,
	} {
		if _, _, err := parseTraefikTraffic([]byte(bad+"\n"), ns); err == nil {
			t.Errorf("riga non valida accettata: %s", bad)
		}
	}
}

func TestTrafficDelta(t *testing.T) {
	prev := trafficCounters{Requests: 100, Errors5xx: 4}
	for name, c := range map[string]struct {
		hadPrev       bool
		cur           trafficCounters
		wantReq, want int64
	}{
		"prima lettura: solo base": {false, trafficCounters{50, 1}, 0, 0},
		"crescita":                 {true, trafficCounters{130, 6}, 30, 2},
		"nessuna richiesta":        {true, trafficCounters{100, 4}, 0, 0},
		"reset di Traefik":         {true, trafficCounters{7, 2}, 7, 2},
		"errori oltre richieste":   {true, trafficCounters{101, 9}, 1, 1},
	} {
		req, errs := trafficDelta(prev, c.hadPrev, c.cur)
		if req != c.wantReq || errs != c.want {
			t.Errorf("%s: %d/%d (voluti %d/%d)", name, req, errs, c.wantReq, c.want)
		}
	}
}

func TestReadTraefikMetrics_SorgenteFintaESenza(t *testing.T) {
	c := New(nil, nil, nil, Config{TraefikMetrics: func(context.Context) ([]byte, error) { return []byte("ok"), nil }})
	if b, err := c.readTraefikMetrics(context.Background()); err != nil || string(b) != "ok" {
		t.Fatalf("%q %v", b, err)
	}
	boom := errors.New("traefik non risponde")
	c = New(nil, nil, nil, Config{TraefikMetrics: func(context.Context) ([]byte, error) { return nil, boom }})
	if _, err := c.readTraefikMetrics(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("errore non propagato: %v", err)
	}
	if _, err := New(nil, nil, nil, Config{}).readTraefikMetrics(context.Background()); err == nil {
		t.Fatal("senza sorgente la lettura risponde errore")
	}
}
