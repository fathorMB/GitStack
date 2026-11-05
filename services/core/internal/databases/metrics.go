package databases

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Metriche dei database gestiti (v3-d, GIT-568, ADR 0003 DB21): CPU e memoria del pod di
// PostgreSQL da metrics-server (metrics.k8s.io), pronto e riavvii dallo stato del pod. Un
// campione senza CPU ne' memoria vuol dire che metrics-server non ha risposto: l'API dirà
// «non disponibile» (available=false con serie vuote), mai zeri. Sul modello di apps/metrics.go.

var podMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

// PodUsage è l'uso di CPU e memoria di un pod, come lo dà metrics-server.
type PodUsage struct {
	CPUMillis   int
	MemoryBytes int64
}

// PodMetricsReader legge l'uso dei pod di un namespace. Nil: l'API metrics.k8s.io. I test la sostituiscono.
type PodMetricsReader func(ctx context.Context, ns string) (map[string]PodUsage, error)

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

// CollectMetrics scrive un campione per ogni database: pronto, riavvii e, se metrics-server ha
// risposto, CPU e memoria del pod di PostgreSQL.
func (c *Controller) CollectMetrics(ctx context.Context) {
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
		c.collectDBMetrics(ctx, d)
	}
}

func (c *Controller) collectDBMetrics(ctx context.Context, d store.ManagedDB) {
	ns := Namespace(d.ID)
	pod, err := c.kube.CoreV1().Pods(ns).Get(ctx, PodName, metav1.GetOptions{})
	if err != nil || pod.DeletionTimestamp != nil || len(pod.Status.ContainerStatuses) == 0 {
		return // il pod non c'e' (ancora): si riprova al prossimo giro
	}
	usage, err := c.readPodMetrics(ctx, ns)
	if err != nil {
		// Niente CPU ne' memoria per questo giro: l'API dirà «non disponibile» se dura.
		usage = nil
	}
	cs := pod.Status.ContainerStatuses[0]
	s := store.ManagedDBMetricSample{At: c.cfg.Now(), Ready: cs.Ready, Restarts: int(cs.RestartCount)}
	if u, ok := usage[pod.Name]; ok {
		cpu, mem := u.CPUMillis, u.MemoryBytes
		s.CPUMillis, s.MemoryBytes = &cpu, &mem
	}
	if err := c.st.AppendManagedDBMetricSamples(ctx, d.ID, []store.ManagedDBMetricSample{s}); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: campioni delle metriche non scritti", "db", d.ID, "err", err)
	}
}
