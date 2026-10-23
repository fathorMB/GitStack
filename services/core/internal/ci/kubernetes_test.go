package ci

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestCorePod_RestrictedELimiti(t *testing.T) {
	pod, err := corePod(Pod{Name: "ci-test", Image: "runner:test", PriorityClass: "gitstack-ci-job", ActiveDeadlineSeconds: 60,
		RunAsUser: 10001, ReadOnlyRootFilesystem: true, CPU: "2", Memory: "4Gi", EphemeralStorage: "20Gi", InitGate: true,
		Env: map[string]string{"B": "2", "A": "1", "GITSTACK_CI_GATEWAY_URL": "http://core:8081", "GITSTACK_CI_GATE_INTERNAL_ADDR": "core:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken || pod.Labels["gitstack.io/ci-job"] != "true" {
		t.Fatal("pod senza le barriere di identita'")
	}
	if pod.Spec.PriorityClassName != "gitstack-ci-job" || *pod.Spec.ActiveDeadlineSeconds != 60 || len(pod.Spec.InitContainers) != 1 ||
		len(pod.Spec.InitContainers[0].Command) != 1 || pod.Spec.InitContainers[0].Command[0] != "/usr/local/bin/gitstack-ci-gate" {
		t.Fatal("pod senza priorita', deadline o gate")
	}
	c := pod.Spec.Containers[0]
	if len(pod.Spec.Volumes) != 2 || c.VolumeMounts[0].MountPath != "/work" || c.VolumeMounts[1].MountPath != "/tmp" ||
		pod.Spec.Volumes[0].EmptyDir.SizeLimit.CmpInt64(20<<30) != 0 {
		t.Fatal("volumi scrivibili del job mancanti o senza il tetto")
	}
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation ||
		c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Fatal("container non restricted")
	}
	for resource, limit := range c.Resources.Limits {
		if got := c.Resources.Requests[resource]; got.Cmp(limit) != 0 {
			t.Fatalf("request %s = %s, want %s", resource, got.String(), limit.String())
		}
	}
	if len(c.Env) != 4 || c.Env[0].Name != "A" || c.Env[3].Name != "GITSTACK_CI_GATEWAY_URL" || c.Env[3].Value != "http://core:8081" {
		t.Fatal("env non deterministico")
	}
	if len(pod.Spec.InitContainers[0].Env) != len(c.Env) || pod.Spec.InitContainers[0].Env[3].Name != "GITSTACK_CI_GATEWAY_URL" {
		t.Fatal("gate senza gli endpoint da verificare")
	}
}

func TestPodConclusion_DeadlineExceeded(t *testing.T) {
	got, done := podConclusion(&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}})
	if !done || got != ConclusionTimedOut {
		t.Fatalf("conclusione deadline: %q, done=%v", got, done)
	}
}
