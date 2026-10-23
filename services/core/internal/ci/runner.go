package ci

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// I limiti sono fissi: il workflow può solo chiedere meno (CI7).
const (
	MaxCPU       = "2"
	MaxMemory    = "4Gi"
	MaxEphemeral = "20Gi"
	MaxRuntime   = 2 * time.Hour
)

// PodCreator è il confine con Kubernetes. L'adattatore usa il client del
// cluster; l'interfaccia mantiene il dispatcher provabile senza un cluster.
type PodCreator interface {
	Create(context.Context, Pod) error
}

// PodWatcher attende la chiusura del pod. Il dispatcher non lascia mai un job
// in_progress dopo una scadenza, un eviction o un errore di scheduling.
type PodWatcher interface {
	Wait(context.Context, string, string) (string, error)
}

// PodDeleter rimuove il pod concluso; non e' richiesto ai finti minimi usati
// dai test del dispatcher.
type PodDeleter interface {
	Delete(context.Context, string, string) error
}

// PodInspector serve alla riconciliazione dopo un riavvio di core.
type PodInspector interface {
	Exists(context.Context, string, string) (bool, error)
}

// Pod è la forma minima, indipendente dal client Kubernetes, del pod effimero.
type Pod struct {
	Name, Namespace, Image, PriorityClass string
	ActiveDeadlineSeconds                 int64
	AutomountServiceAccountToken          bool
	RunAsUser                             int64
	AllowPrivilegeEscalation              bool
	ReadOnlyRootFilesystem                bool
	DropAllCapabilities                   bool
	InitGate                              bool
	CPU, Memory, EphemeralStorage         string
	Command                               []string
	Env                                   map[string]string
}

// RunnerConfig contiene solo i dati che l'amministratore può scegliere.
type RunnerConfig struct {
	Namespace, Image, GatewayURL, ActionMirror string
	Capacity                                   int
}

func (c RunnerConfig) normal() RunnerConfig {
	if c.Namespace == "" {
		c.Namespace = "gitstack-ci-jobs"
	}
	if c.Capacity < 1 {
		c.Capacity = 3
	}
	if c.Capacity > 3 {
		c.Capacity = 3
	}
	return c
}

// PodForJob crea un pod restricted e rootless. Il gate, incluso nell'immagine,
// è fail-closed finché la policy non rende irraggiungibile il cluster.
func PodForJob(c RunnerConfig, job JobRow, run RunRow) (Pod, error) {
	c = c.normal()
	if strings.TrimSpace(c.Image) == "" {
		return Pod{}, fmt.Errorf("immagine runner CI obbligatoria")
	}
	name := "ci-" + strings.ReplaceAll(job.ID.String(), "-", "")
	return Pod{Name: name, Namespace: c.Namespace, Image: c.Image, PriorityClass: "gitstack-ci-job",
		ActiveDeadlineSeconds: int64(MaxRuntime / time.Second), AutomountServiceAccountToken: false,
		RunAsUser: 10001, AllowPrivilegeEscalation: false, ReadOnlyRootFilesystem: true,
		DropAllCapabilities: true, CPU: MaxCPU, Memory: MaxMemory, EphemeralStorage: MaxEphemeral,
		InitGate: true,
		Command:  []string{"/usr/local/bin/ci-runner"},
		Env: map[string]string{"GITSTACK_CI_GATEWAY_URL": c.GatewayURL,
			"GITSTACK_CI_ACTION_MIRROR": c.ActionMirror, "GITSTACK_CI_RUNNER": "act-host"}}, nil
}

// PodForJobTicket aggiunge solo al processo supervisore il biglietto monouso
// emesso per questo job. Il ci-runner lo elimina prima di avviare act.
func PodForJobTicket(c RunnerConfig, job JobRow, run RunRow, ticket string) (Pod, error) {
	p, err := PodForJob(c, job, run)
	if err != nil {
		return Pod{}, err
	}
	if strings.TrimSpace(ticket) == "" {
		return Pod{}, fmt.Errorf("biglietto CI obbligatorio")
	}
	p.Env["GITSTACK_CI_RUNNER_SECRET"] = ticket
	return p, nil
}

// DispatchOnce prende un solo job interno e ne riflette l'esito nel modello
// della coda. I runner esterni non vengono mai assegnati a un pod interno.
func DispatchOnce(ctx context.Context, pool *pgxpool.Pool, api interface {
	PodCreator
	PodWatcher
}, cfg RunnerConfig, now time.Time) (bool, error) {
	job, run, ok, err := ClaimNext(ctx, pool, now, cfg.normal().Capacity, "internal-pod")
	if err != nil || !ok {
		return ok, err
	}
	if job.RunnerType != "internal_pod" {
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	pod, err := PodForJob(cfg, job, run)
	if err != nil {
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	if err := api.Create(ctx, pod); err != nil {
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	conclusion, err := api.Wait(ctx, pod.Namespace, pod.Name)
	if err != nil || conclusion == "" {
		conclusion = ConclusionFailure
	}
	if conclusion != ConclusionSuccess && conclusion != ConclusionFailure && conclusion != ConclusionTimedOut {
		conclusion = ConclusionFailure
	}
	return true, CompleteJob(ctx, pool, time.Now().UTC(), job.ID, conclusion)
}

// DispatchPods riempie i posti liberi senza aspettare la fine dei pod gia' creati.
// ClaimNext serializza il conteggio globale, quindi piu' repliche di core non
// superano Capacity. Ogni osservatore chiude e poi elimina il proprio pod.
func DispatchPods(ctx context.Context, pool *pgxpool.Pool, api interface {
	PodCreator
	PodWatcher
}, cfg RunnerConfig, now time.Time, rep Reporter) (int, error) {
	started := 0
	for {
		job, run, ticket, ok, err := ClaimNextWithTicketReported(ctx, pool, now, cfg.normal().Capacity, "internal-pod", rep)
		if err != nil || !ok {
			return started, err
		}
		pod, err := PodForJobTicket(cfg, job, run, ticket)
		if err != nil || job.RunnerType != "internal_pod" || api.Create(ctx, pod) != nil {
			if cerr := CompleteJobReported(ctx, pool, now, job.ID, ConclusionFailure, rep); cerr != nil {
				return started, cerr
			}
			continue
		}
		started++
		go observePod(ctx, pool, api, pod, job.ID, rep)
	}
}

func observePod(ctx context.Context, pool *pgxpool.Pool, api PodWatcher, pod Pod, jobID uuid.UUID, rep Reporter) {
	conclusion, err := api.Wait(ctx, pod.Namespace, pod.Name)
	if err != nil || (conclusion != ConclusionSuccess && conclusion != ConclusionFailure && conclusion != ConclusionTimedOut) {
		conclusion = ConclusionFailure
	}
	_ = CompleteJobReported(context.WithoutCancel(ctx), pool, time.Now().UTC(), jobID, conclusion, rep)
	if deleter, ok := api.(PodDeleter); ok {
		_ = deleter.Delete(context.WithoutCancel(ctx), pod.Namespace, pod.Name)
	}
}

// Reconcile chiude i job in corso il cui pod non esiste piu' (per esempio
// dopo un riavvio o una cancellazione). Il gancio ticketExpired verra' usato
// dalla presa monouso: per ora e' nil e non cambia lo stato dei job validi.
func Reconcile(ctx context.Context, pool *pgxpool.Pool, pods PodInspector, namespace string, ticketExpired func(JobRow) bool, now time.Time, rep Reporter) error {
	rows, err := pool.Query(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE status = 'in_progress' AND runner_type = 'internal_pod'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return err
		}
		missing := ticketExpired != nil && ticketExpired(job)
		if !missing {
			exists, err := pods.Exists(ctx, namespace, "ci-"+strings.ReplaceAll(job.ID.String(), "-", ""))
			if err != nil {
				return err
			}
			missing = !exists
		}
		if missing {
			if err := CompleteJobReported(ctx, pool, now, job.ID, ConclusionFailure, rep); err != nil {
				return err
			}
			continue
		}
		// Dopo il riavvio nessuna goroutine sta piu' osservando un pod vivo:
		// riprendiamo l'attesa prima di prendere altri job dalla coda.
		if watcher, ok := pods.(PodWatcher); ok {
			pod := Pod{Name: "ci-" + strings.ReplaceAll(job.ID.String(), "-", ""), Namespace: namespace}
			go observePod(ctx, pool, watcher, pod, job.ID, rep)
		}
	}
	return rows.Err()
}
