//go:build integration

package ci_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/google/uuid"
)

// quotaPods imita la ResourceQuota ci-jobs con pods=1: Create e' rifiutata finche' esiste
// un altro pod. Con deletes=true il finto sa eliminare i pod (come l'adattatore Kubernetes).
type quotaPods struct {
	mu       sync.Mutex
	live     map[string]bool
	created  []string
	rejected int
	deletes  bool
}

func newQuotaPods(deletes bool) *quotaPods {
	return &quotaPods{live: map[string]bool{}, deletes: deletes}
}

func (q *quotaPods) Create(_ context.Context, p ci.Pod) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.live) > 0 {
		q.rejected++
		return errors.New("pods \"" + p.Name + "\" is forbidden: exceeded quota: ci-jobs, requested: pods=1, used: pods=1, limited: pods=1")
	}
	q.live[p.Name] = true
	q.created = append(q.created, p.Name)
	return nil
}

func (q *quotaPods) Wait(context.Context, string, string) (string, error) {
	return ci.ConclusionSuccess, nil
}

func (q *quotaPods) release() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.live = map[string]bool{}
}

func (q *quotaPods) Exists(_ context.Context, _, name string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.live[name], nil
}

// quotaPodsDel aggiunge Delete: il pod sparisce solo dopo un attimo, come in Kubernetes.
type quotaPodsDel struct{ *quotaPods }

func (q quotaPodsDel) Delete(_ context.Context, _, name string) error {
	go func() {
		time.Sleep(150 * time.Millisecond)
		q.mu.Lock()
		delete(q.live, name)
		q.mu.Unlock()
	}()
	return nil
}

func needsRun(t *testing.T, e *env) (a, b uuid.UUID) {
	t.Helper()
	repo := e.repo(uuid.New(), "needs")
	e.src.files[sha("a")] = []ci.File{{Path: ".github/workflows/n.yml", Content: []byte(
		"on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n  test:\n    needs: build\n    runs-on: ubuntu-latest\n")}}
	ids, err := e.eng.OnPush(context.Background(), pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("accodamento: %v %v", ids, err)
	}
	for key, dst := range map[string]*uuid.UUID{"build": &a, "test": &b} {
		if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.ci_jobs WHERE run_id = $1 AND job_key = $2`, ids[0], key).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	return a, b
}

func dispatchUntil(t *testing.T, e *env, pods interface {
	ci.PodCreator
	ci.PodWatcher
}, cfg ci.RunnerConfig, jobID uuid.UUID, want string, each func()) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ci.DispatchPods(context.Background(), e.pool, pods, cfg, *e.clock, &captureReporter{}); err != nil {
			t.Fatal(err)
		}
		var got *string
		_ = e.pool.QueryRow(context.Background(), `SELECT conclusion FROM core.ci_jobs WHERE id = $1`, jobID).Scan(&got)
		if got != nil {
			if *got != want {
				t.Fatalf("job %s chiuso con %q, atteso %q", jobID, *got, want)
			}
			return
		}
		if each != nil {
			each()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s non chiuso con %q", jobID, want)
}

// GIT-452: capacita' 1, B (needs A). Il pod di A esiste ancora quando B viene preso e
// Create rifiuta per quota: B non va in failure, torna in coda e riparte quando c'e' posto.
func TestDispatchPods_QuotaRimetteInCoda(t *testing.T) {
	e := newEnv(t)
	_, b := needsRun(t, e)
	pods := newQuotaPods(false) // niente Delete: il posto in quota resta occupato finche' non lo liberiamo
	var logbuf strings.Builder
	var mu sync.Mutex
	cfg := ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1,
		Log: slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &logbuf}, nil)), Logs: &ci.LogStore{Dir: t.TempDir()}}
	ticks := 0
	dispatchUntil(t, e, pods, cfg, b, ci.ConclusionSuccess, func() {
		ticks++
		if ticks == 6 { // dopo alcuni rifiuti il pod di A sparisce
			pods.release()
		}
	})
	pods.mu.Lock()
	rejected, created := pods.rejected, len(pods.created)
	pods.mu.Unlock()
	if rejected < 2 || created != 2 {
		t.Errorf("rifiuti=%d (attesi >=2), pod creati=%d (attesi 2)", rejected, created)
	}
	mu.Lock()
	logged := logbuf.String()
	mu.Unlock()
	if !strings.Contains(logged, "reason=pod_quota") || strings.Count(logged, "reason=pod_quota") != 1 {
		t.Errorf("attesa una sola riga reason=pod_quota: %s", logged)
	}
	if strings.Contains(logged, "reason=pod_create") {
		t.Errorf("la quota non e' un pod_create: %s", logged)
	}
}

// GIT-452: se il posto non si libera entro QuotaRetryTimeout il job chiude in failure.
func TestDispatchPods_QuotaScade(t *testing.T) {
	e := newEnv(t)
	_, b := needsRun(t, e)
	pods := newQuotaPods(false)
	var logbuf strings.Builder
	var mu sync.Mutex
	cfg := ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1,
		Log: slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &logbuf}, nil)), Logs: &ci.LogStore{Dir: t.TempDir()}}
	old := ci.QuotaRetryTimeout
	ci.QuotaRetryTimeout = 5 * time.Minute
	defer func() { ci.QuotaRetryTimeout = old }()
	start := *e.clock
	step := 0
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		now := start.Add(time.Duration(step) * time.Minute)
		step++
		if _, err := ci.DispatchPods(context.Background(), e.pool, pods, cfg, now, &captureReporter{}); err != nil {
			t.Fatal(err)
		}
		var got *string
		_ = e.pool.QueryRow(context.Background(), `SELECT conclusion FROM core.ci_jobs WHERE id = $1`, b).Scan(&got)
		if got != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitJobConclusion(t, e, b, ci.ConclusionFailure)
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logbuf.String(), "reason=pod_quota_timeout") {
		t.Errorf("manca reason=pod_quota_timeout: %s", logbuf.String())
	}
}

// GIT-452: con Delete, il posto non si libera prima che il pod sia sparito: B parte al
// primo tentativo, senza nessun rifiuto di quota.
func TestDispatchPods_PostoLiberoSoloDopoDelete(t *testing.T) {
	e := newEnv(t)
	_, b := needsRun(t, e)
	pods := newQuotaPods(true)
	cfg := ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1}
	dispatchUntil(t, e, quotaPodsDel{pods}, cfg, b, ci.ConclusionSuccess, nil)
	pods.mu.Lock()
	defer pods.mu.Unlock()
	if pods.rejected != 0 || len(pods.created) != 2 {
		t.Errorf("rifiuti=%d (attesi 0), pod creati=%d (attesi 2)", pods.rejected, len(pods.created))
	}
}
