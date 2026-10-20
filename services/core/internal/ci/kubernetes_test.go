package ci

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestCorePod_RestrictedELimiti(t *testing.T) {
	pod, err := corePod(Pod{Name: "ci-test", Image: "runner:test", PriorityClass: "gitstack-ci-job", ActiveDeadlineSeconds: 60,
		RunAsUser: 10001, ReadOnlyRootFilesystem: true, CPU: "2", Memory: "4Gi", EphemeralStorage: "20Gi", InitGate: true,
		Env: map[string]string{"B": "2", "A": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken || pod.Labels["gitstack.io/ci-job"] != "true" {
		t.Fatal("pod senza le barriere di identita'")
	}
	if pod.Spec.PriorityClassName != "gitstack-ci-job" || *pod.Spec.ActiveDeadlineSeconds != 60 || len(pod.Spec.InitContainers) != 1 {
		t.Fatal("pod senza priorita', deadline o gate")
	}
	c := pod.Spec.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation ||
		c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Fatal("container non restricted")
	}
	for resource, limit := range c.Resources.Limits {
		if got := c.Resources.Requests[resource]; got.Cmp(limit) != 0 {
			t.Fatalf("request %s = %s, want %s", resource, got.String(), limit.String())
		}
	}
	if len(c.Env) != 2 || c.Env[0].Name != "A" {
		t.Fatal("env non deterministico")
	}
}
