package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Metriche minime delle app (v3-a, GIT-384, H12): CPU, memoria e riavvii per copia. CPU e memoria
// vengono da metrics-server (metrics.k8s.io); riavvii e stato di pronto dal pod. Un campione senza
// CPU né memoria vuol dire che metrics-server non ha risposto: l'API lo dice «non disponibile», non
// mostra zeri. Richieste ed errori 5xx vengono da Traefik (traffic.go, GIT-403).

const defaultMetricsEvery = 30 * time.Second

// PodUsage è l'uso di CPU e memoria di un pod, come lo dà metrics-server.
type PodUsage struct {
	CPUMillis   int
	MemoryBytes int64
}

// PodMetricsReader legge l'uso dei pod di un namespace. Nil: l'API metrics.k8s.io. I test la sostituiscono.
type PodMetricsReader func(ctx context.Context, ns string) (map[string]PodUsage, error)

var podMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

// readPodMetrics legge l'uso dei pod di un namespace. Un errore vuol dire «sorgente non risponde».
func (c *Controller) readPodMetrics(ctx context.Context, ns string) (map[string]PodUsage, error) {
	if c.cfg.PodMetrics != nil {
		return c.cfg.PodMetrics(ctx, ns)
	}
	if c.dyn == nil {
		return nil, fmt.Errorf("metrics-server non configurato")
	}
	list, err := c.dyn.Resource(podMetricsGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]PodUsage, len(list.Items))
	for _, item := range list.Items {
		u, err := parsePodMetrics(item.Object)
		if err != nil {
			return nil, fmt.Errorf("metriche del pod %s: %w", item.GetName(), err)
		}
		out[item.GetName()] = u
	}
	return out, nil
}

// parsePodMetrics somma CPU e memoria dei container di un oggetto PodMetrics (quantità Kubernetes,
// per esempio «3m» e «20Mi»).
func parsePodMetrics(obj map[string]any) (PodUsage, error) {
	var m struct {
		Containers []struct {
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return PodUsage{}, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return PodUsage{}, err
	}
	var u PodUsage
	for _, c := range m.Containers {
		cpu, err := resource.ParseQuantity(c.Usage.CPU)
		if err != nil {
			return PodUsage{}, fmt.Errorf("cpu %q: %w", c.Usage.CPU, err)
		}
		mem, err := resource.ParseQuantity(c.Usage.Memory)
		if err != nil {
			return PodUsage{}, fmt.Errorf("memoria %q: %w", c.Usage.Memory, err)
		}
		u.CPUMillis += int(cpu.MilliValue())
		u.MemoryBytes += mem.Value()
	}
	return u, nil
}

// CollectMetrics scrive un campione per copia di ogni app.
func (c *Controller) CollectMetrics(ctx context.Context) {
	ids, err := c.st.AppIDs(ctx)
	if err != nil {
		return
	}
	// Una lettura di Traefik per giro: ne escono i contatori di tutte le app.
	traefik, terr := c.readTraefikMetrics(ctx)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		app, err := c.st.GetAppByID(ctx, id)
		if err != nil {
			continue
		}
		c.collectAppMetrics(ctx, app)
		if terr == nil {
			c.collectTraffic(ctx, id, traefik, c.cfg.Now())
		}
	}
}

func (c *Controller) collectAppMetrics(ctx context.Context, app store.App) {
	ns := Namespace(app.ID)
	pods, err := c.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + WorkloadName})
	if err != nil {
		return // namespace non ancora creato, o cluster non raggiungibile: si riprova
	}
	usage, err := c.readPodMetrics(ctx, ns)
	if err != nil {
		// Niente CPU né memoria per questo giro: l'API dirà «non disponibile» se dura.
		usage = nil
	}
	now := c.cfg.Now()
	samples := make([]store.AppMetricSample, 0, len(pods.Items))
	for _, p := range pods.Items {
		if p.DeletionTimestamp != nil || len(p.Status.ContainerStatuses) == 0 {
			continue
		}
		cs := p.Status.ContainerStatuses[0]
		s := store.AppMetricSample{Copy: p.Name, At: now, Ready: cs.Ready, Restarts: int(cs.RestartCount)}
		if u, ok := usage[p.Name]; ok {
			cpu, mem := u.CPUMillis, u.MemoryBytes
			s.CPUMillis, s.MemoryBytes = &cpu, &mem
		}
		samples = append(samples, s)
	}
	if err := c.st.AppendAppMetricSamples(ctx, app.ID, samples); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: campioni delle metriche non scritti", "app", app.ID, "err", err)
	}
}
