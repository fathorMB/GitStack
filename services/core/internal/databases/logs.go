package databases

import (
	"bufio"
	"context"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Log dei database gestiti (v3-d, GIT-568, ADR 0003 DB21). Il controller raccoglie le righe di
// PostgreSQL da pods/log e le scrive nello store, come fa apps.Controller.CollectLogs per le
// copie delle app: la cronologia vive nel database di core, non nel pod, quindi resta leggibile
// dopo la sostituzione del pod, con la stessa retention dei log delle app (7 giorni o 1 GiB per
// database, regolabile). Postgres non scrive mai le password nei log (l'autenticazione è a
// livello di protocollo), quindi qui non c'è un masker: le righe si conservano come arrivano.

const (
	logTailLines       = 1000
	logScanBufferBytes = 1 << 20
	logLineMaxBytes    = 16 << 10
	logTruncatedMarker = "…"
	logTimestampLayout = time.RFC3339Nano
)

// readPodLogs legge le righe di un container di un pod (con i timestamp, come `kubectl logs --timestamps`).
// Nil: l'API di Kubernetes. I test la sostituiscono.
func (c *Controller) readPodLogs(ctx context.Context, ns, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	if c.cfg.PodLogs != nil {
		return c.cfg.PodLogs(ctx, ns, pod, opts)
	}
	return c.kube.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
}

// CollectLogs raccoglie le righe nuove del pod di ogni database e le scrive nello store.
func (c *Controller) CollectLogs(ctx context.Context) {
	dbs, err := c.st.ListAllManagedDBs(ctx)
	if err != nil {
		return
	}
	for _, d := range dbs {
		if ctx.Err() != nil {
			return
		}
		if d.Status == store.DBDeleting {
			continue
		}
		c.collectDBLogs(ctx, d)
	}
}

func (c *Controller) collectDBLogs(ctx context.Context, d store.ManagedDB) {
	ns := Namespace(d.ID)
	pod, err := c.kube.CoreV1().Pods(ns).Get(ctx, PodName, metav1.GetOptions{})
	if err != nil || pod.DeletionTimestamp != nil || len(pod.Spec.Containers) == 0 {
		return // il pod non c'e' (ancora): si riprova al prossimo giro
	}
	last, err := c.st.LastManagedDBLogTime(ctx, d.ID)
	if err != nil {
		return
	}
	// Deduplica su (at, message): SinceTime è troncato al secondo, quindi la lettura può
	// riguardare l'istante dell'ultima riga già scritta; una raffica spezzata fra due raccolte
	// non perde righe nuove con lo stesso microsecondo.
	lastMsgs := map[string]bool{}
	if !last.IsZero() {
		msgs, err := c.st.ManagedDBLogMessagesAt(ctx, d.ID, last)
		if err != nil {
			return
		}
		for _, m := range msgs {
			lastMsgs[m] = true
		}
	}
	opts := &corev1.PodLogOptions{Container: WorkloadName, Timestamps: true}
	if last.IsZero() {
		tail := int64(logTailLines)
		opts.TailLines = &tail
	} else {
		since := metav1.NewTime(last.Truncate(time.Second))
		opts.SinceTime = &since
	}
	rc, err := c.readPodLogs(ctx, ns, pod.Name, opts)
	if err != nil {
		return // il pod non ha ancora log (si sta avviando): si riprova
	}
	defer func() { _ = rc.Close() }()
	var lines []store.NewManagedDBLogLine
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), logScanBufferBytes)
	now := c.cfg.Now()
	for sc.Scan() {
		at, text := parseLogLine(sc.Text(), now)
		if !last.IsZero() && (at.Before(last) || (at.Equal(last) && lastMsgs[text])) {
			continue
		}
		lines = append(lines, store.NewManagedDBLogLine{Time: at, Message: capLogLine(text)})
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: lettura dei log interrotta", "db", d.ID, "err", err)
	}
	if len(lines) == 0 {
		return
	}
	if err := c.st.AppendManagedDBLogs(ctx, d.ID, lines); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: righe di log non scritte", "db", d.ID, "err", err)
	}
}

// PruneLogs toglie le righe e i campioni oltre la scadenza e oltre il limite di dimensione per
// database: la stessa retention dei log delle app (ADR 0002).
func (c *Controller) PruneLogs(ctx context.Context) {
	now := c.cfg.Now()
	if _, err := c.st.PruneManagedDBLogs(ctx, now.Add(-c.cfg.LogRetention), c.cfg.LogMaxBytes); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: pulizia dei log non riuscita", "err", err)
	}
	if _, err := c.st.PruneManagedDBMetrics(ctx, now.Add(-c.cfg.LogRetention)); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: pulizia delle metriche non riuscita", "err", err)
	}
}

// parseLogLine separa il timestamp RFC 3339 che Kubernetes mette in testa a ogni riga. Senza, la
// riga prende l'orario della raccolta.
func parseLogLine(line string, fallback time.Time) (time.Time, string) {
	ts, text, ok := strings.Cut(line, " ")
	if !ok {
		return fallback.UTC().Truncate(time.Microsecond), line
	}
	at, err := time.Parse(logTimestampLayout, ts)
	if err != nil {
		return fallback.UTC().Truncate(time.Microsecond), line
	}
	return at.UTC().Truncate(time.Microsecond), text
}

// capLogLine limita una riga alla dimensione massima, senza spezzare un carattere UTF-8.
func capLogLine(s string) string {
	if len(s) <= logLineMaxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:logLineMaxBytes], "") + logTruncatedMarker
}
