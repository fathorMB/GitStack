//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-384 (v3-a, H12): GET /apps/{app}/metrics. CPU, memoria e riavvii per copia; senza campioni con
// CPU la risposta è available=false con serie vuote (non zeri); richieste ed errori 5xx (Traefik, GIT-403) restano null finché non c'è un campione.

func TestAppMetrics_SerieRiavvitiECopie(t *testing.T) {
	e := newAppsEnv(t)
	ctx := context.Background()
	repo := e.repo("app", false)
	app := createTestApp(t, e.pool, repo, "alice", "web", "alice")
	createTestApp(t, e.pool, repo, "alice", "api", "alice") // senza campioni
	st := store.New(e.pool)
	now := time.Now().UTC().Truncate(time.Second)
	cpu := func(v int) *int { return &v }
	mem := func(v int64) *int64 { return &v }
	if err := st.AppendAppMetricSamples(ctx, app.ID, []store.AppMetricSample{
		{Copy: "web-1", At: now.Add(-2 * time.Minute), Ready: true, Restarts: 0, CPUMillis: cpu(10), MemoryBytes: mem(100)},
		{Copy: "web-1", At: now.Add(-time.Minute), Ready: true, Restarts: 1, CPUMillis: cpu(20), MemoryBytes: mem(200)},
		{Copy: "web-2", At: now.Add(-time.Minute), Ready: false, Restarts: 0},
	}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(http.MethodGet, "/repos/alice/app/apps/web/metrics?range=1h", "alice", "")
	e.want(rec, http.StatusOK, "")
	var m openapi.AppMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if !m.Available || m.Range != "1h" || m.StepSeconds != 60 {
		t.Fatalf("intestazione delle metriche: available=%v range=%v step=%d", m.Available, m.Range, m.StepSeconds)
	}
	if m.Cpu.Unit != openapi.Millicores || len(m.Cpu.Points) == 0 || m.Memory.Unit != openapi.Bytes {
		t.Fatalf("serie CPU/memoria: %+v %+v", m.Cpu, m.Memory)
	}
	if m.Restarts24h != 1 {
		t.Errorf("riavvii nelle ultime 24 ore = %d (voluto 1)", m.Restarts24h)
	}
	if len(m.Copies) != 2 {
		t.Errorf("copie = %+v (voluto web-1 e web-2)", m.Copies)
	}
	if m.Requests != nil || m.Errors5xx != nil {
		t.Errorf("richieste ed errori senza campioni di Traefik restano null: %+v %+v", m.Requests, m.Errors5xx)
	}

	// Un'app senza campioni: non disponibile, serie vuote, nessun zero inventato.
	rec = e.do(http.MethodGet, "/repos/alice/app/apps/api/metrics", "alice", "")
	e.want(rec, http.StatusOK, "")
	var none openapi.AppMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &none); err != nil {
		t.Fatal(err)
	}
	if none.Available || len(none.Cpu.Points) != 0 || len(none.Copies) != 0 || none.Range != "1h" {
		t.Errorf("senza campioni: %+v", none)
	}

	// Un intervallo che non esiste è 400; chi non legge il repo non vede le metriche.
	e.want(e.do(http.MethodGet, "/repos/alice/app/apps/web/metrics?range=2h", "alice", ""), http.StatusBadRequest, "")
	// (il valore non ammesso arriva dal gestore, non dal contratto: 400 con il campo nel messaggio)
	if rec := e.do(http.MethodGet, "/repos/alice/app/apps/web/metrics", "bob", ""); rec.Code == http.StatusOK {
		t.Errorf("bob non legge il repo privato: %d", rec.Code)
	}
}

// GIT-403 (H12): le differenze di richieste ed errori 5xx scritte dal controller escono come serie
// non null, sommate per passo; un'app senza campioni di Traefik le ha null.
func TestAppMetrics_RichiesteEdErrori5xx(t *testing.T) {
	e := newAppsEnv(t)
	ctx := context.Background()
	repo := e.repo("app", false)
	app := createTestApp(t, e.pool, repo, "alice", "web", "alice")
	createTestApp(t, e.pool, repo, "alice", "api", "alice")
	st := store.New(e.pool)
	now := time.Now().UTC().Truncate(time.Second)
	for i, d := range []struct{ req, errs int64 }{{0, 0}, {40, 3}, {25, 0}, {10, 10}} {
		if err := st.AppendAppTrafficSample(ctx, app.ID, store.AppTrafficSample{At: now.Add(-time.Duration(4-i) * 10 * time.Second), Requests: d.req, Errors5xx: d.errs}); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.do(http.MethodGet, "/repos/alice/app/apps/web/metrics?range=1h", "alice", "")
	e.want(rec, http.StatusOK, "")
	var m openapi.AppMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Requests == nil || m.Errors5xx == nil {
		t.Fatalf("richieste ed errori non devono essere null con campioni: %s", rec.Body.String())
	}
	if m.Requests.Unit != openapi.Count || m.Errors5xx.Unit != openapi.Count {
		t.Errorf("unità: %v %v", m.Requests.Unit, m.Errors5xx.Unit)
	}
	var req, errs float32
	for _, p := range m.Requests.Points {
		req += p.Value
	}
	for _, p := range m.Errors5xx.Points {
		errs += p.Value
	}
	if req != 75 || errs != 13 {
		t.Errorf("totali: %v richieste (volute 75), %v errori 5xx (voluti 13)", req, errs)
	}
	// Senza campioni di Traefik: null, non zeri.
	rec = e.do(http.MethodGet, "/repos/alice/app/apps/api/metrics", "alice", "")
	e.want(rec, http.StatusOK, "")
	var none openapi.AppMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &none); err != nil {
		t.Fatal(err)
	}
	if none.Requests != nil || none.Errors5xx != nil {
		t.Errorf("senza campioni le serie sono null: %+v %+v", none.Requests, none.Errors5xx)
	}
}
