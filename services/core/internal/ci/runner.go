package ci

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	Namespace, Image, ActionMirror string
	Capacity                       int
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
		Command:  []string{"/usr/local/bin/gitstack-ci-job", "--job", job.ID.String(), "--run", run.ID.String()},
		Env:      map[string]string{"GITSTACK_CI_ACTION_MIRROR": c.ActionMirror, "GITSTACK_CI_RUNNER": "act-host"}}, nil
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
