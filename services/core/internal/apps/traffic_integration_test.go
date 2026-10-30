//go:build integration

package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// GIT-403 (H12): CollectMetrics legge i contatori di Traefik e scrive le differenze per app.
func TestAppMetrics_RichiesteDaTraefikConDifferenzeERiavvio(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	svc := Namespace(l.app.ID) + "-" + WorkloadName + "-80@kubernetescrd"
	text := func(ok, fail int) []byte {
		s := "# TYPE traefik_service_requests_total counter\n"
		if ok+fail > 0 {
			s += fmt.Sprintf("traefik_service_requests_total{code=\"200\",method=\"GET\",protocol=\"http\",service=\"%s\"} %d\n", svc, ok)
			s += fmt.Sprintf("traefik_service_requests_total{code=\"500\",method=\"POST\",protocol=\"http\",service=\"%s\"} %d\n", svc, fail)
		}
		s += `traefik_service_requests_total{code="500",method="GET",protocol="http",service="altra-app-80@kubernetescrd"} 999` + "\n"
		return s2b(s)
	}
	var body []byte
	var srcErr error
	l.ctl.cfg.PodMetrics = func(context.Context, string) (map[string]PodUsage, error) {
		return nil, errors.New("senza metrics-server")
	}
	l.ctl.cfg.TraefikMetrics = func(context.Context) ([]byte, error) { return body, srcErr }

	sum := func() (req, errs int64, n int) {
		bs, err := l.st.AppTrafficBuckets(ctx, l.app.ID, time.Now().Add(-time.Hour), 3600)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bs {
			req += b.Requests
			errs += b.Errors5xx
		}
		var rows int
		if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM core.app_traffic_samples WHERE app_id = $1`, l.app.ID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return req, errs, rows
	}

	// 1. Traefik risponde ma non ha ancora visto richieste: base a zero, un campione a zero.
	body = text(0, 0)
	l.ctl.CollectMetrics(ctx)
	if req, errs, rows := sum(); rows != 1 || req != 0 || errs != 0 {
		t.Fatalf("primo giro: %d richieste, %d errori, %d righe", req, errs, rows)
	}
	// 2. Arrivano 10 richieste, 2 con 5xx (altre app non contano).
	body = text(8, 2)
	l.ctl.CollectMetrics(ctx)
	if req, errs, rows := sum(); rows != 2 || req != 10 || errs != 2 {
		t.Fatalf("secondo giro: %d richieste, %d errori, %d righe (volute 10, 2, 2)", req, errs, rows)
	}
	// 3. La sorgente non risponde: nessuna riga nuova.
	srcErr = errors.New("traefik non risponde")
	l.ctl.CollectMetrics(ctx)
	if _, _, rows := sum(); rows != 2 {
		t.Fatalf("sorgente assente: %d righe (volute 2)", rows)
	}
	srcErr = nil
	// 4. Traefik riavviato: i contatori ripartono, vale il valore attuale.
	body = text(3, 0)
	l.ctl.CollectMetrics(ctx)
	if req, errs, rows := sum(); rows != 3 || req != 13 || errs != 2 {
		t.Fatalf("dopo il reset: %d richieste, %d errori, %d righe (volute 13, 2, 3)", req, errs, rows)
	}
}

func s2b(s string) []byte { return []byte(s) }
