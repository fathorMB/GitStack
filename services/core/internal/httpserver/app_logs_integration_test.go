//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// GIT-350 (v3-a, H5/H12): log in diretta e cronologia, pulizia per età e dimensione, segreti
// mascherati (GIT-348). Le copie non girano su un cluster di test: le righe arrivano da un lettore
// finto (apps.Config.PodLogs) e il controller le raccoglie con CollectLogs.

// fakePodLogs è il lettore delle righe delle copie: per pod, le righe (`RFC3339Nano testo`).
type fakePodLogs struct {
	mu    sync.Mutex
	lines map[string][]string
}

func (f *fakePodLogs) set(pod string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines[pod] = lines
}

func (f *fakePodLogs) read(_ context.Context, _, pod string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return io.NopCloser(strings.NewReader(strings.Join(f.lines[pod], "\n") + "\n")), nil
}

type logsEnv struct {
	*appsEnv
	kube  *fake.Clientset
	ctl   *apps.Controller
	pods  *fakePodLogs
	store *store.Store
}

// newLogsEnv: router con portachiavi (i segreti si cifrano), controller con cluster finto e lettore
// delle righe finto, orologio finto.
func newLogsEnv(t *testing.T) *logsEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base := &issuesEnv{t: t, pool: pool, id: id}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	keys, err := webhooks.NewKeyring("k1", strings.Repeat("ab", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	kube := fake.NewSimpleClientset()
	pods := &fakePodLogs{lines: map[string][]string{}}
	ctl := apps.New(st, kube, nil, apps.Config{
		Now: clock.Now, PodLogs: pods.read,
		Secrets: keys,
	})
	base.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}), httpserver.WithAppController(ctl))
	return &logsEnv{appsEnv: &appsEnv{issuesEnv: base, clock: clock, git: git}, kube: kube, ctl: ctl, pods: pods, store: st}
}

// ts è l'istante del clock più off, in RFC 3339 con decimali (come li scrive Kubernetes).
func (e *logsEnv) ts(off time.Duration) string {
	return e.clock.Now().Add(off).Format(time.RFC3339Nano)
}

// appID legge l'id dell'app dall'API.
func (e *logsEnv) appID(path string) uuid.UUID {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, "alice", "")
	e.want(rec, http.StatusOK, "")
	var a openapi.App
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		e.t.Fatal(err)
	}
	return uuid.UUID(a.Id)
}

// podsOf mette nel cluster finto una copia per nome dell'app e ne fissa le righe.
func (e *logsEnv) podsOf(app uuid.UUID, lines map[string][]string) {
	e.t.Helper()
	for name, ls := range lines {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: apps.Namespace(app),
				Labels: map[string]string{"app.kubernetes.io/name": apps.WorkloadName}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: apps.WorkloadName}}},
		}
		if _, err := e.kube.CoreV1().Pods(apps.Namespace(app)).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
			e.t.Fatal(err)
		}
		e.pods.set(name, ls...)
	}
}

// doCtx è do con un contesto della richiesta: la diretta finisce quando finisce il contesto.
func (e *logsEnv) doCtx(ctx context.Context, path, user string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// TestAppLogs_Permessi: leggere i log richiede write (H5); read riceve 403, un repo che non si legge 404.
func TestAppLogs_Permessi(t *testing.T) {
	e := newLogsEnv(t)
	repoID := e.repo("handbook", true) // internal: carol legge, non scrive
	e.id.grantWrite(repoID, "bob")
	e.repo("riservato", false)
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")

	e.want(e.do(http.MethodGet, base+"/web/logs", "carol", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base+"/web/logs", "bob", ""), http.StatusOK, "")
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps/web/logs", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodGet, base+"/nessuna/logs", "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, base+"/web/logs?limit=0", "bob", ""), http.StatusBadRequest, "bad_request")
}

// TestAppLogs_SegretoMascheratoInDirettaEInCronologia: l'app stampa il valore di un segreto; la riga
// arriva con *** sia in cronologia sia in diretta, mentre una variabile resta in chiaro.
func TestAppLogs_SegretoMascheratoInDirettaEInCronologia(t *testing.T) {
	e := newLogsEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	const secret = "s3cr3t-valore-di-prova-42"
	const variable = "eu-west-visibile"
	e.want(e.do(http.MethodPut, base+"/web/secrets/API_TOKEN", "alice", `{"value":"`+secret+`"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, base+"/web/variables/REGION", "alice", `{"value":"`+variable+`"}`), http.StatusOK, "")
	app := e.appID(base + "/web")
	const pod = "web-7f9c-abcde"

	e.podsOf(app, map[string][]string{pod: {
		e.ts(-2*time.Second) + " avvio, token=" + secret,
		e.ts(-1*time.Second) + " REGION=" + variable,
	}})
	e.ctl.CollectLogs(context.Background())

	hist := e.do(http.MethodGet, base+"/web/logs", "bob", "").Body.String()
	if !strings.Contains(hist, "avvio, token=***") {
		t.Fatalf("cronologia senza il segreto mascherato: %q", hist)
	}
	if strings.Contains(hist, secret) {
		t.Fatalf("il valore del segreto compare in cronologia: %q", hist)
	}
	if !strings.Contains(hist, "REGION="+variable) {
		t.Fatalf("la variabile deve restare in chiaro: %q", hist)
	}

	// Diretta: la richiesta resta aperta per 3 secondi; a 1,2 secondi l'app stampa una riga nuova
	// con il segreto, che la raccolta porta nello store e la diretta manda mascherata.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.doCtx(ctx, base+"/web/logs?follow=true", "bob") }()
	time.Sleep(1200 * time.Millisecond)
	e.pods.set(pod,
		e.ts(-2*time.Second)+" avvio, token="+secret,
		e.ts(-1*time.Second)+" REGION="+variable,
		e.ts(0)+" connessione al database con password="+secret)
	e.ctl.CollectLogs(context.Background())
	rec := <-done
	live := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("diretta: risposta %d", rec.Code)
	}
	if !strings.Contains(live, "connessione al database con password=***") {
		t.Fatalf("la riga nuova della diretta non è arrivata mascherata: %q", live)
	}
	if !strings.Contains(live, "avvio, token=***") {
		t.Fatalf("la diretta non ha mandato anche la cronologia: %q", live)
	}
	if strings.Contains(live, secret) {
		t.Fatalf("il valore del segreto compare in diretta: %q", live)
	}

	nd := e.do(http.MethodGet, base+"/web/logs?format=ndjson", "bob", "").Body.String()
	if strings.Contains(nd, secret) || !strings.Contains(nd, "***") {
		t.Fatalf("ndjson non mascherato: %q", nd)
	}
}

// TestAppLogs_CronologiaScadenzaEDimensione: con un orologio finto, le righe oltre 7 giorni spariscono
// e, oltre il limite di byte per app, si tengono le più recenti.
func TestAppLogs_CronologiaScadenzaEDimensione(t *testing.T) {
	e := newLogsEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	app := e.appID(base + "/web")

	// Due righe recenti della stessa lunghezza (B, C) e una più vecchia (A), più una oltre sette giorni.
	const msgA = "riga A aaaaaaaaaaaaaaaaaaaaaaaaa"
	const msgB = "riga B bbbbbbbbbbbbbbbbbbbbbbbbb"
	const msgC = "riga C ccccccccccccccccccccccccc"
	e.podsOf(app, map[string][]string{"web-1-aaaaa": {
		e.ts(-8*24*time.Hour) + " vecchia oltre sette giorni",
		e.ts(-1*time.Hour) + " " + msgA,
		e.ts(-30*time.Minute) + " " + msgB,
		e.ts(-1*time.Minute) + " " + msgC,
	}})
	e.ctl.CollectLogs(context.Background())

	// Limite per app di due righe (più un byte): esce per dimensione la più vecchia tra le recenti.
	small := apps.New(e.store, fake.NewSimpleClientset(), nil, apps.Config{
		Now: e.clock.Now, LogRetention: 7 * 24 * time.Hour, LogMaxBytes: int64(2*len(msgB) + 1),
	})
	small.PruneLogs(context.Background())

	hist := e.do(http.MethodGet, base+"/web/logs", "bob", "").Body.String()
	if strings.Contains(hist, "vecchia") {
		t.Fatalf("oltre i sette giorni la riga deve sparire: %q", hist)
	}
	if strings.Contains(hist, msgA) {
		t.Fatalf("oltre il limite di dimensione esce la riga più vecchia: %q", hist)
	}
	if !strings.Contains(hist, msgB) || !strings.Contains(hist, msgC) {
		t.Fatalf("restano le righe più recenti, entro il limite: %q", hist)
	}
	// Filtri: grep (sottostringa), copia, sorgente e limite (le più recenti).
	if g := e.do(http.MethodGet, base+"/web/logs?grep=riga%20C", "bob", "").Body.String(); !strings.Contains(g, msgC) || strings.Contains(g, msgB) {
		t.Fatalf("grep: %q", g)
	}
	if c := e.do(http.MethodGet, base+"/web/logs?copy=web-1-aaaaa", "bob", "").Body.String(); !strings.Contains(c, msgB) {
		t.Fatalf("copy: %q", c)
	}
	if c := e.do(http.MethodGet, base+"/web/logs?copy=altra", "bob", "").Body.String(); strings.TrimSpace(c) != "" {
		t.Fatalf("copy inesistente deve dare vuoto: %q", c)
	}
	if s := e.do(http.MethodGet, base+"/web/logs?source=platform", "bob", "").Body.String(); strings.TrimSpace(s) != "" {
		t.Fatalf("source=platform senza eventi deve dare vuoto: %q", s)
	}
	if l := e.do(http.MethodGet, base+"/web/logs?limit=1", "bob", "").Body.String(); !strings.Contains(l, msgC) || strings.Contains(l, msgB) {
		t.Fatalf("limit=1 deve dare solo la riga più recente: %q", l)
	}

	// Otto giorni dopo (orologio finto): B e C escono per età.
	e.clock.advance(8 * 24 * time.Hour)
	small.PruneLogs(context.Background())
	if body := e.do(http.MethodGet, base+"/web/logs", "bob", "").Body.String(); strings.TrimSpace(body) != "" {
		t.Fatalf("dopo sette giorni la cronologia deve essere vuota: %q", body)
	}
}

// TestAppLogs_SenzaSegretiNonSiRaccolgono: se i segreti non si leggono, le righe delle copie non si
// scrivono (niente può finire in chiaro).
func TestAppLogs_SenzaSegretiNonSiRaccolgono(t *testing.T) {
	e := newLogsEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	app := e.appID(base + "/web")
	e.podsOf(app, map[string][]string{"web-2-bbbbb": {e.ts(-time.Second) + " riga che non deve comparire"}})

	senza := apps.New(e.store, e.kube, nil, apps.Config{Now: e.clock.Now, PodLogs: e.pods.read})
	senza.CollectLogs(context.Background())
	if body := e.do(http.MethodGet, base+"/web/logs", "bob", "").Body.String(); strings.Contains(body, "non deve comparire") {
		t.Fatalf("senza segreti letti le righe delle copie non devono essere raccolte: %q", body)
	}
}

// TestAppLogs_EventiDiPiattaforma: un riavvio per memoria (OOMKilled) diventa una riga della
// piattaforma, una sola per riavvio; un riavvio per un altro motivo non produce nulla.
func TestAppLogs_EventiDiPiattaforma(t *testing.T) {
	e := newLogsEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	app := e.appID(base + "/web")
	ctx := context.Background()

	// setStatus imposta il riavvio e il motivo dell'ultima terminazione di una copia.
	setStatus := func(pod string, restarts int32, reason string) {
		t.Helper()
		p, err := e.kube.CoreV1().Pods(apps.Namespace(app)).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: apps.WorkloadName, RestartCount: restarts,
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 137}},
		}}
		if _, err := e.kube.CoreV1().Pods(apps.Namespace(app)).UpdateStatus(ctx, p, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	const oomPod, errPod = "web-oom-ccccc", "web-err-ddddd"
	const oomLine = "terminata per memoria insufficiente (OOMKilled)"
	e.podsOf(app, map[string][]string{oomPod: {e.ts(-time.Second) + " avvio"}, errPod: {e.ts(-time.Second) + " avvio"}})

	platform := func() string {
		return e.do(http.MethodGet, base+"/web/logs?source=platform", "bob", "").Body.String()
	}

	setStatus(oomPod, 1, "OOMKilled")
	e.ctl.CollectLogs(ctx)
	e.ctl.CollectLogs(ctx) // una seconda raccolta non ripete l'evento
	if n := strings.Count(platform(), oomLine); n != 1 {
		t.Fatalf("dopo un riavvio OOM e due raccolte: %d righe, voluta 1: %q", n, platform())
	}

	setStatus(oomPod, 2, "OOMKilled")
	e.ctl.CollectLogs(ctx)
	if n := strings.Count(platform(), oomLine); n != 2 {
		t.Fatalf("il secondo riavvio OOM deve dare una riga in più: %d righe, voluta 2: %q", n, platform())
	}

	setStatus(errPod, 3, "Error")
	e.ctl.CollectLogs(ctx)
	if p := platform(); strings.Contains(p, errPod) || strings.Count(p, oomLine) != 2 {
		t.Fatalf("un riavvio per errore non deve produrre righe di piattaforma: %q", p)
	}
}
