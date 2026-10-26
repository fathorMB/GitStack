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
// CPU la risposta è available=false con serie vuote (non zeri); richieste ed errori restano null.

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
		t.Errorf("richieste ed errori non sono raccolti in questa versione: %+v %+v", m.Requests, m.Errors5xx)
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
