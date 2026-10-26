package apps

import (
	"bufio"
	"context"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/appenv"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Log delle app (v3-a, GIT-350, H12). Il controller raccoglie le righe delle copie (i pod) e le
// scrive nello store; gli eventi della piattaforma (deploy, riavvio, scala, stato, OOM) vanno nello
// stesso posto. Ogni riga di una copia passa dal masker dei segreti dell'app prima di essere scritta
// (appenv.Masker, GIT-348): senza segreti letti, le righe delle copie non si raccolgono affatto, così
// un valore non finisce mai in chiaro nella cronologia.

const (
	defaultLogsEvery    = 2 * time.Second
	defaultLogRetention = 7 * 24 * time.Hour
	defaultLogMaxBytes  = int64(1) << 30
	logTailLines        = 1000
	logLineMaxBytes     = 16 << 10
	logScanBufferBytes  = 1 << 20
	logTruncatedMarker  = "…"
	oomKilledReason     = "OOMKilled"
	logTimestampLayout  = time.RFC3339Nano
)

// PodLogReader legge le righe di un container di un pod (con i timestamp, come `kubectl logs --timestamps`).
// Nil: l'API di Kubernetes. I test la sostituiscono.
type PodLogReader func(ctx context.Context, ns, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error)

func (c *Controller) readPodLogs(ctx context.Context, ns, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	if c.cfg.PodLogs != nil {
		return c.cfg.PodLogs(ctx, ns, pod, opts)
	}
	return c.kube.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
}

// platform scrive un evento della piattaforma nei log dell'app. Un errore non ferma il deploy: si registra.
func (c *Controller) platform(ctx context.Context, appID uuid.UUID, msg string) {
	err := c.st.AppendAppLogs(ctx, appID, []store.NewAppLogLine{{
		Time: c.cfg.Now(), Source: store.AppLogSourcePlatform, Message: msg,
	}})
	if err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: evento della piattaforma non scritto nei log", "app", appID, "err", err)
	}
}

// CollectLogs raccoglie le righe nuove di tutte le copie di tutte le app e le scrive mascherate.
func (c *Controller) CollectLogs(ctx context.Context) {
	ids, err := c.st.AppIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		app, err := c.st.GetAppByID(ctx, id)
		if err != nil {
			continue
		}
		c.collectApp(ctx, app)
	}
}

func (c *Controller) collectApp(ctx context.Context, app store.App) {
	ns := Namespace(app.ID)
	pods, err := c.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + WorkloadName})
	if err != nil {
		return // namespace non ancora creato, o cluster non raggiungibile: si riprova
	}
	if c.cfg.Secrets == nil {
		// Senza i segreti non si sa cosa mascherare: niente righe delle copie. Gli eventi restano.
		c.collectOOM(ctx, app, pods.Items)
		return
	}
	secrets, err := c.st.AppSecretValues(ctx, c.cfg.Secrets, app.ID)
	if err != nil {
		c.cfg.Log.Warn("apps: segreti non letti, righe delle copie non raccolte", "app", app.ID, "err", err)
		c.collectOOM(ctx, app, pods.Items)
		return
	}
	mask := appenv.Masker(secrets)
	for _, p := range pods.Items {
		if p.DeletionTimestamp != nil || len(p.Spec.Containers) == 0 {
			continue
		}
		c.collectPod(ctx, app, ns, p, mask)
	}
	c.collectOOM(ctx, app, pods.Items)
}

// collectPod scrive le righe nuove di una copia. Il confronto è con l'ultima riga già scritta (al
// microsecondo): le righe della lettura che non sono più nuove si saltano.
func (c *Controller) collectPod(ctx context.Context, app store.App, ns string, p corev1.Pod, mask *ci.Masker) {
	name := p.Name
	last, err := c.st.LastAppLogTime(ctx, app.ID, name)
	if err != nil {
		return
	}
	opts := &corev1.PodLogOptions{Container: p.Spec.Containers[0].Name, Timestamps: true}
	if last.IsZero() {
		tail := int64(logTailLines)
		opts.TailLines = &tail
	} else {
		since := metav1.NewTime(last.Truncate(time.Second))
		opts.SinceTime = &since
	}
	rc, err := c.readPodLogs(ctx, ns, name, opts)
	if err != nil {
		return // la copia non ha ancora log (si sta avviando): si riprova
	}
	defer func() { _ = rc.Close() }()
	var lines []store.NewAppLogLine
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), logScanBufferBytes)
	now := c.cfg.Now()
	for sc.Scan() {
		at, text := parseLogLine(sc.Text(), now)
		if !last.IsZero() && !at.After(last) {
			continue
		}
		copyName := name
		lines = append(lines, store.NewAppLogLine{
			Time: at, Source: store.AppLogSourceApp, Copy: &copyName, Message: capLine(mask.Mask(text)),
		})
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: lettura dei log di una copia interrotta", "app", app.ID, "copy", name, "err", err)
	}
	if len(lines) == 0 {
		return
	}
	if err := c.st.AppendAppLogs(ctx, app.ID, lines); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: righe di log non scritte", "app", app.ID, "copy", name, "err", err)
	}
}

// collectOOM registra una volta per riavvio le copie terminate per memoria (OOMKilled): è l'evento
// che un utente non vede nei log del programma.
func (c *Controller) collectOOM(ctx context.Context, app store.App, pods []corev1.Pod) {
	c.mu.Lock()
	seen := c.oomSeen[app.ID]
	if seen == nil {
		seen = map[string]int32{}
		c.oomSeen[app.ID] = seen
	}
	c.mu.Unlock()
	for _, p := range pods {
		for _, cs := range p.Status.ContainerStatuses {
			t := cs.LastTerminationState.Terminated
			if t == nil || t.Reason != oomKilledReason || cs.RestartCount <= seen[p.Name] {
				continue
			}
			seen[p.Name] = cs.RestartCount
			c.platform(ctx, app.ID, "copia "+p.Name+" terminata per memoria insufficiente (OOMKilled)")
		}
	}
}

// PruneLogs toglie le righe oltre la scadenza e oltre il limite di dimensione per app.
func (c *Controller) PruneLogs(ctx context.Context) {
	now := c.cfg.Now()
	if _, err := c.st.PruneAppLogs(ctx, now.Add(-c.cfg.LogRetention), c.cfg.LogMaxBytes); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: pulizia dei log non riuscita", "err", err)
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

// capLine limita una riga alla dimensione massima, senza spezzare un carattere UTF-8.
func capLine(s string) string {
	if len(s) <= logLineMaxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:logLineMaxBytes], "") + logTruncatedMarker
}
