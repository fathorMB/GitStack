//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DB21 (GIT-568): telemetria dei database. Le prove girano senza Postgres vero (API e controller
// con cluster finto e lettori falsi), come i test dei log delle app: solo i test toccati, con -run mirato.

// fakeDBPodLogs è il lettore finto delle righe del pod del database, per nome del pod.
type fakeDBPodLogs struct {
	mu    sync.Mutex
	lines map[string][]string
}

func (f *fakeDBPodLogs) set(pod string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines[pod] = lines
}

func (f *fakeDBPodLogs) read(_ context.Context, _, pod string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return io.NopCloser(strings.NewReader(strings.Join(f.lines[pod], "\n") + "\n")), nil
}

// fakeDBMetrics è la sorgente finta di metrics-server: uso per nome del pod.
type fakeDBMetrics struct {
	mu    sync.Mutex
	usage map[string]databases.PodUsage
	err   error
}

func (f *fakeDBMetrics) read(_ context.Context, _ string) (map[string]databases.PodUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]databases.PodUsage, len(f.usage))
	for k, v := range f.usage {
		out[k] = v
	}
	return out, nil
}

// telemetryEnv è un dbEnv con i lettori falsi di log e metriche.
type telemetryEnv struct {
	*dbEnv
	pods    *fakeDBPodLogs
	metrics *fakeDBMetrics
}

func newTelemetryEnv(t *testing.T) *telemetryEnv {
	t.Helper()
	kube := fake.NewSimpleClientset()
	sql := &sqlSpy{}
	pods := &fakeDBPodLogs{lines: map[string][]string{}}
	metrics := &fakeDBMetrics{usage: map[string]databases.PodUsage{}}
	proxy := &lateController{}
	holder := &dbEnv{kube: kube, sql: sql, proxy: proxy}
	holder.appsEnv = newAppsEnv(t, httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy}))
	holder.ctl = databases.New(store.New(holder.pool), kube, databases.Config{
		SQL: sql, Images: map[int]databases.Image{}, PodLogs: pods.read, PodMetrics: metrics.read,
	})
	proxy.set(holder.ctl)
	return &telemetryEnv{dbEnv: holder, pods: pods, metrics: metrics}
}

// dbPod mette nel cluster finto il pod pg-0 del database con gli stati dei container.
func (e *telemetryEnv) dbPod(id uuid.UUID, restarts int32, ready bool) {
	e.t.Helper()
	ns := databases.Namespace(id)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: databases.PodName, Namespace: ns,
			Labels: map[string]string{"app.kubernetes.io/name": databases.WorkloadName}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: databases.WorkloadName}}},
	}
	if ready {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: databases.WorkloadName, Ready: true, RestartCount: restarts}}
	} else {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: databases.WorkloadName, RestartCount: restarts}}
	}
	if _, err := e.kube.CoreV1().Pods(ns).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *telemetryEnv) metricsGet(path, user string) (int, openapi.DatabaseMetrics) {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	var m openapi.DatabaseMetrics
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			e.t.Fatalf("%v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, m
}

// DB21: con una sorgente metrics-server (finta) CPU/RAM sono presenti e available=true; senza
// sorgente available=false con serie vuote, mentre dimensione e riavvii restano. Chi non è admin
// dell'owner non legge (404, DB2).
func TestDatabases_MetricheCpuRamDaMetricsServer(t *testing.T) {
	e := newTelemetryEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)

	// Senza campioni: la sorgente si dichiara non disponibile, le serie sono vuote (non zeri).
	code, m := e.metricsGet("/databases/alice/shop/metrics", "alice")
	if code != http.StatusOK || m.Available || len(m.Cpu.Points) != 0 || len(m.Memory.Points) != 0 {
		t.Fatalf("senza campioni: %d %+v", code, m)
	}
	st := store.New(e.pool)
	if _, err := st.SetManagedDBUsage(context.Background(), id, 900<<20, store.DiskStateFor(900<<20, 1<<30)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetManagedDBRestarts(context.Background(), id, 2); err != nil {
		t.Fatal(err)
	}
	// Il pod non c'è ancora: la raccolta non scrive campioni.
	e.ctl.CollectMetrics(context.Background())
	_, m = e.metricsGet("/databases/alice/shop/metrics", "alice")
	if m.Available || len(m.Cpu.Points) != 0 || len(m.DatabaseBytes.Points) != 1 || m.Restarts24h != 2 {
		t.Fatalf("dimensione e riavvii restano senza CPU/RAM: %+v", m)
	}
	if m.Usage == nil || m.Usage.DiskState != "warning" {
		t.Fatalf("uso: %+v", m.Usage)
	}

	// Con il pod e la sorgente: CPU/RAM presenti e available=true.
	e.dbPod(id, 3, true)
	e.metrics.usage[databases.PodName] = databases.PodUsage{CPUMillis: 137, MemoryBytes: 52 << 20}
	e.ctl.CollectMetrics(context.Background())
	_, m = e.metricsGet("/databases/alice/shop/metrics", "alice")
	if !m.Available || len(m.Cpu.Points) == 0 || len(m.Memory.Points) == 0 {
		t.Fatalf("con la sorgente: %+v", m)
	}
	if m.Cpu.Points[0].Value != 137 || m.Memory.Points[0].Value != float32(52<<20) {
		t.Fatalf("valori: %+v %+v", m.Cpu.Points, m.Memory.Points)
	}
	if m.Restarts24h != 2 { // restarts24h viene dal modello, non dai campioni
		t.Fatalf("riavvii: %+v", m)
	}

	// La sorgente che smette di rispondere non azzera le serie passate: available resta vero
	// finche' i campioni con CPU restano nella finestra; il campione nuovo, senza CPU, non aggiunge punti.
	e.metrics.err = errors.New("metrics-server non raggiungibile")
	e.ctl.CollectMetrics(context.Background())
	_, m = e.metricsGet("/databases/alice/shop/metrics", "alice")
	if !m.Available || len(m.Cpu.Points) != 1 {
		t.Fatalf("campioni vecchi nella finestra: %+v", m)
	}

	// Chi non è admin dell'owner non legge le metriche (DB2).
	e.want(e.do(http.MethodGet, "/databases/alice/shop/metrics", "bob", ""), http.StatusNotFound, "not_found")
}

// DB21: i log restano leggibili dopo la sostituzione del pod: le righe raccolte vivono nello
// store, non nel pod. La retention è quella dei log delle app: il prune per età e dimensione.
func TestDatabases_LogConservatiDopoSostituzionePod(t *testing.T) {
	e := newTelemetryEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)

	ts := e.clock.Now().UTC()
	e.dbPod(id, 0, true)
	e.pods.set(databases.PodName,
		ts.Add(-2*time.Minute).Format(time.RFC3339Nano)+" database system is ready",
		ts.Add(-time.Minute).Format(time.RFC3339Nano)+" connection authorized: user=shop_owner",
	)
	e.ctl.CollectLogs(context.Background())

	// Primo pod, due righe in cronologia (anche dopo che il pod sparisce).
	rec := e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", "")
	e.want(rec, http.StatusOK, "")
	if body := rec.Body.String(); !strings.Contains(body, "database system is ready") || !strings.Contains(body, "connection authorized") {
		t.Fatalf("cronologia: %q", body)
	}

	// Sostituzione del pod: il nuovo pod ha solo le righe nuove; le vecchie restano nello store.
	if err := e.kube.CoreV1().Pods(databases.Namespace(id)).Delete(context.Background(), databases.PodName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	e.dbPod(id, 1, true)
	e.pods.set(databases.PodName, ts.Format(time.RFC3339Nano)+" checkpoint starting")
	e.ctl.CollectLogs(context.Background())
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", "")
	e.want(rec, http.StatusOK, "")
	body := rec.Body.String()
	for _, want := range []string{"database system is ready", "connection authorized", "checkpoint starting"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dopo la sostituzione manca %q: %q", want, body)
		}
	}
	// Il riavvio del pod è osservato dal controller: i log nuovi arrivano comunque.
	e.ctl.CollectLogs(context.Background())

	// ndjson: un oggetto DatabaseLogLine per riga, con l'orario.
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs?format=ndjson", "alice", "")
	e.want(rec, http.StatusOK, "")
	var line struct {
		Time    time.Time `json:"time"`
		Message string    `json:"message"`
	}
	rows := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(rows) != 3 {
		t.Fatalf("righe ndjson: %d", len(rows))
	}
	if err := json.Unmarshal([]byte(rows[0]), &line); err != nil || !line.Time.Equal(ts.Add(-2*time.Minute)) || line.Message != "database system is ready" {
		t.Fatalf("riga ndjson: %+v %v", line, err)
	}

	// grep e limit come dai parametri del contratto.
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs?grep=checkpoint", "alice", "")
	e.want(rec, http.StatusOK, "")
	if body := rec.Body.String(); strings.Contains(body, "ready") || !strings.Contains(body, "checkpoint") {
		t.Fatalf("grep: %q", body)
	}
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs?limit=2", "alice", "")
	e.want(rec, http.StatusOK, "")
	if n := strings.Count(strings.TrimSpace(rec.Body.String()), "\n") + 1; n != 2 {
		t.Fatalf("limit: %d righe", n)
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/logs?since=5giorni", "alice", ""), http.StatusBadRequest, "bad_request")
	e.want(e.do(http.MethodGet, "/databases/alice/shop/logs?format=xml", "alice", ""), http.StatusBadRequest, "bad_request")

	// Retention come i log delle app: una riga vecchia di 8 giorni sparisce con il prune,
	// le recenti restano; anche i campioni metrici hanno la stessa scadenza.
	st := store.New(e.pool)
	if err := st.AppendManagedDBLogs(context.Background(), id, []store.NewManagedDBLogLine{
		{Time: e.clock.Now().Add(-8 * 24 * time.Hour), Message: "riga vecchia"},
	}); err != nil {
		t.Fatal(err)
	}
	e.ctl.PruneLogs(context.Background())
	rec = e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", "")
	e.want(rec, http.StatusOK, "")
	if body := rec.Body.String(); strings.Contains(body, "riga vecchia") {
		t.Fatalf("la retention non ha potato: %q", body)
	}

	// Chi non è admin dell'owner non legge i log (DB2).
	e.want(e.do(http.MethodGet, "/databases/alice/shop/logs", "bob", ""), http.StatusNotFound, "not_found")
}

// Una raffica spezzata fra due raccolte non perde righe: la deduplica è su
// (at, message), non sul solo istante (SinceTime è troncato al secondo).
func TestDatabases_LogRafficaStessoIstante(t *testing.T) {
	e := newTelemetryEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)

	ts := e.clock.Now().UTC()
	e.dbPod(id, 0, true)
	e.pods.set(databases.PodName, ts.Add(-time.Minute).Format(time.RFC3339Nano)+" prima riga")
	e.ctl.CollectLogs(context.Background())

	// Seconda lettura: la riga già scritta (stesso istante, stesso testo) più due righe nuove
	// con lo stesso microsecondo dell'ultima salvata: nessuna si perde, nessuna si duplica.
	e.pods.set(databases.PodName,
		ts.Add(-time.Minute).Format(time.RFC3339Nano)+" prima riga",
		ts.Add(-time.Minute).Format(time.RFC3339Nano)+" raffica nuova",
		ts.Format(time.RFC3339Nano)+" checkpoint starting",
	)
	e.ctl.CollectLogs(context.Background())

	rec := e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", "")
	e.want(rec, http.StatusOK, "")
	body := rec.Body.String()
	if strings.Count(body, "prima riga") != 1 || strings.Count(body, "raffica nuova") != 1 || strings.Count(body, "checkpoint starting") != 1 {
		t.Fatalf("raffica: %q", body)
	}
}
