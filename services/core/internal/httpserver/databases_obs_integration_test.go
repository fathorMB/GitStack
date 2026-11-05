//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DB22: fra il commit della riga e Provision passa un giro del reconciler che crea il Secret con la sua
// password. La risposta di creazione deve mostrare la password del Secret (quella applicata ai ruoli).
func TestDatabases_PasswordDellaRispostaEQuellaDelSecret(t *testing.T) {
	e := newDBEnv(t)
	e.proxy.beforeProvision = func(ctx context.Context, d store.ManagedDB) {
		// Il controller di prova non ha immagini: basta che prepari namespace e Secret come farebbe il giro vero.
		if _, err := e.ctl.Provision(ctx, d, "password-del-reconciler"); err != nil {
			t.Error(err)
		}
	}
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	sec, err := e.kube.CoreV1().Secrets(databases.Namespace(uuid.UUID(created.Database.Id))).Get(context.Background(), databases.SecretAuth, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(sec.Data["owner-password"]) != "password-del-reconciler" || created.Credentials.Password != "password-del-reconciler" {
		t.Fatalf("risposta %q, Secret %q", created.Credentials.Password, sec.Data["owner-password"])
	}
}

// DB21: metriche con sorgente assente (serie vuote, available=false) e presente (serie CPU/RAM/dimensione).
func TestDatabases_MetricheConESenzaMetricsServer(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)
	st := store.New(e.pool)
	get := func(q string) openapi.DatabaseMetrics {
		rec := e.do(http.MethodGet, "/databases/alice/shop/metrics"+q, "alice", "")
		e.want(rec, http.StatusOK, "")
		var m openapi.DatabaseMetrics
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := get(""); m.Available || len(m.Cpu.Points) != 0 || len(m.Memory.Points) != 0 {
		t.Fatalf("senza campioni: %+v", m)
	}
	now := time.Now().UTC()
	size := int64(900 << 20)
	// Solo dimensione (metrics-server assente): CPU/RAM restano vuote e non disponibili, non zero.
	if err := st.AppendManagedDBSample(context.Background(), id, store.ManagedDBSample{At: now.Add(-2 * time.Minute), DBBytes: &size}); err != nil {
		t.Fatal(err)
	}
	m := get("")
	if m.Available || len(m.Cpu.Points) != 0 || len(m.DatabaseBytes.Points) != 1 {
		t.Fatalf("solo dimensione: %+v", m)
	}
	cpu, mem := 42, int64(100<<20)
	if err := st.AppendManagedDBSample(context.Background(), id, store.ManagedDBSample{At: now.Add(-time.Minute), CPUMillis: &cpu, MemoryBytes: &mem, DBBytes: &size}); err != nil {
		t.Fatal(err)
	}
	m = get("?range=6h")
	if !m.Available || len(m.Cpu.Points) != 1 || m.Cpu.Points[0].Value != 42 || len(m.Memory.Points) != 1 || m.Memory.Points[0].Value != float32(100<<20) {
		t.Fatalf("con metrics-server: %+v", m)
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/metrics?range=9h", "alice", ""), http.StatusBadRequest, "")
	e.want(e.do(http.MethodGet, "/databases/alice/shop/metrics", "bob", ""), http.StatusNotFound, "not_found")
}

// DB21: i log conservati si leggono dall'API anche quando il pod non c'e' piu'; grep, limite e ndjson.
func TestDatabases_LogConservatiDopoLaSostituzioneDelPod(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)
	st := store.New(e.pool)
	base := time.Now().UTC().Add(-time.Minute)
	if err := st.AppendManagedDBLogs(context.Background(), id, []store.ManagedDBLogLine{
		{At: base, Message: "LOG: avvio"}, {At: base.Add(time.Second), Message: "ERROR: relazione mancante"}, {At: base.Add(2 * time.Second), Message: "LOG: pronto"}}); err != nil {
		t.Fatal(err)
	}
	// Nel cluster finto il pod non esiste: le righe vengono comunque dallo store.
	rec := e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", "")
	e.want(rec, http.StatusOK, "")
	if got := strings.Split(strings.TrimSpace(rec.Body.String()), "\n"); len(got) != 3 || got[0] != "LOG: avvio" {
		t.Fatalf("log: %q", rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs?grep=error&format=ndjson", "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"message":"ERROR: relazione mancante"`) || strings.Count(rec.Body.String(), "\n") != 1 {
		t.Fatalf("ndjson con grep: %q", rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs?limit=1", "alice", "")
	if strings.TrimSpace(rec.Body.String()) != "LOG: pronto" {
		t.Fatalf("limit = le piu' recenti: %q", rec.Body.String())
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/logs", "bob", ""), http.StatusNotFound, "not_found")
}
