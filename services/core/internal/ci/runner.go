package ci

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// Pod è la forma minima, indipendente dal client Kubernetes, del pod effimero.
type Pod struct {
	Name, Namespace, Image, PriorityClass string
	ActiveDeadlineSeconds                 int64
	AutomountServiceAccountToken          bool
	RunAsUser                             int64
	AllowPrivilegeEscalation              bool
	ReadOnlyRootFilesystem                bool
	DropAllCapabilities                   bool
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
		Command: []string{"/usr/local/bin/gitstack-ci-job", "--job", job.ID.String(), "--run", run.ID.String()},
		Env:     map[string]string{"GITSTACK_CI_ACTION_MIRROR": c.ActionMirror, "GITSTACK_CI_RUNNER": "act-host"}}, nil
}
