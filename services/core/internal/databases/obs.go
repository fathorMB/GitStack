package databases

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Metriche e log (DB21). CPU/RAM da metrics-server (se manca o non risponde il campione non le ha: l'API
// dice «non disponibile», niente zeri); dimensione da pg_database_size; riavvii dallo stato del pod. Le
// righe di pods/log si conservano nel database di core, quindi si recuperano dopo la sostituzione del pod.

// sample scrive un campione. size nil = pg_database_size non letto.
func (c *Controller) sample(ctx context.Context, d store.ManagedDB, size *int64) {
	m := store.ManagedDBSample{At: c.cfg.Now().Truncate(time.Second), DBBytes: size}
	if c.cfg.PodMetrics != nil {
		if cpu, mem, err := c.cfg.PodMetrics(ctx, Namespace(d.ID)); err == nil {
			m.CPUMillis, m.MemoryBytes = &cpu, &mem
		}
	}
	if err := c.st.AppendManagedDBSample(ctx, d.ID, m); err != nil {
		c.cfg.Log.Warn("databases: campione non scritto", "db", d.ID, "err", err)
	}
	if err := c.st.PurgeManagedDBObservability(ctx, c.cfg.Now().Add(-c.cfg.LogRetention)); err != nil {
		c.cfg.Log.Warn("databases: retention non applicata", "db", d.ID, "err", err)
	}
}

// collectLogs raccoglie le righe nuove del pod (con timestamp) e le scrive; riparte dall'ultima conservata,
// quindi un pod sostituito non perde la cronologia e non si duplicano righe.
func (c *Controller) collectLogs(ctx context.Context, d store.ManagedDB) {
	c.mu.Lock()
	last := c.logAt[d.ID]
	due := c.cfg.Now().Sub(last) >= c.cfg.LogEvery
	if due {
		c.logAt[d.ID] = c.cfg.Now()
	}
	c.mu.Unlock()
	if !due {
		return
	}
	if err := c.CollectLogs(ctx, d); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: log non raccolti", "db", d.ID, "err", err)
	}
}

// CollectLogs fa un giro di raccolta (esportato per i test).
func (c *Controller) CollectLogs(ctx context.Context, d store.ManagedDB) error {
	lastAt, err := c.st.LastManagedDBLogTime(ctx, d.ID)
	if err != nil {
		return err
	}
	opts := &corev1.PodLogOptions{Container: WorkloadName, Timestamps: true}
	if !lastAt.IsZero() {
		t := metav1.NewTime(lastAt)
		opts.SinceTime = &t
	}
	rc, err := c.Logs(ctx, d, opts)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	return c.storeLogLines(ctx, d.ID, bufio.NewScanner(rc), lastAt)
}

func (c *Controller) storeLogLines(ctx context.Context, id uuid.UUID, sc *bufio.Scanner, after time.Time) error {
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var lines []store.ManagedDBLogLine
	for sc.Scan() {
		ts, msg, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			continue
		}
		at = at.Truncate(time.Microsecond)
		if !after.IsZero() && !at.After(after) {
			continue
		}
		if len(msg) > 16<<10 {
			msg = msg[:16<<10] + "…"
		}
		lines = append(lines, store.ManagedDBLogLine{At: at.UTC(), Message: msg})
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return c.st.AppendManagedDBLogs(ctx, id, lines)
}

var podMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

// MetricsFromDynamic legge CPU e RAM del pod del database da metrics-server (metrics.k8s.io). Un errore
// (metrics-server assente o senza dati) vuol dire «non disponibile».
func MetricsFromDynamic(dyn dynamic.Interface) func(ctx context.Context, ns string) (int, int64, error) {
	return func(ctx context.Context, ns string) (int, int64, error) {
		item, err := dyn.Resource(podMetricsGVR).Namespace(ns).Get(ctx, PodName, metav1.GetOptions{})
		if err != nil {
			return 0, 0, err
		}
		containers, _, _ := unstructured.NestedSlice(item.Object, "containers")
		var cpu, mem int64
		for _, c := range containers {
			m, _ := c.(map[string]any)
			usage, _ := m["usage"].(map[string]any)
			cq, err := resource.ParseQuantity(fmt.Sprint(usage["cpu"]))
			if err != nil {
				return 0, 0, err
			}
			mq, err := resource.ParseQuantity(fmt.Sprint(usage["memory"]))
			if err != nil {
				return 0, 0, err
			}
			cpu += cq.MilliValue()
			mem += mq.Value()
		}
		return int(cpu), mem, nil
	}
}
