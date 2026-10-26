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
	if len(c.Env) != 4 || c.Env[0].Name != "A" || c.Env[1].Name != "B" {
		t.Fatalf("env non deterministico: %+v", c.Env)
	}
	lookup := func(env []corev1.EnvVar, name string) string {
		for _, v := range env {
			if v.Name == name {
				return v.Value
			}
		}
		return ""
	}
	if lookup(c.Env, "GITSTACK_CI_GATEWAY_URL") != "http://core:8081" || lookup(c.Env, "GITSTACK_CI_GATE_INTERNAL_ADDR") != "core:8080" {
		t.Fatalf("endpoint del gate assente: %+v", c.Env)
	}
	if len(pod.Spec.InitContainers[0].Env) != len(c.Env) || lookup(pod.Spec.InitContainers[0].Env, "GITSTACK_CI_GATE_INTERNAL_ADDR") != "core:8080" {
		t.Fatal("gate senza gli endpoint da verificare")
	}
}

func TestPodConclusion_DeadlineExceeded(t *testing.T) {
	got, done := podConclusion(&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}})
	if !done || got != ConclusionTimedOut {
		t.Fatalf("conclusione deadline: %q, done=%v", got, done)
	}
}

// Il pod di build deve passare la ValidatingAdmissionPolicy del namespace di build
// (deploy/gitstack/templates/ci-build.yaml): le stesse regole, controllate qui sul pod vero.
func TestCorePod_BuildRispettaLaPolicyDiAmmissione(t *testing.T) {
	pod, err := corePod(Pod{Name: "ci-b", Namespace: "gitstack-ci-build", Image: "runner:test", PriorityClass: "gitstack-ci-job", ActiveDeadlineSeconds: 60,
		RunAsUser: BuildUser, ReadOnlyRootFilesystem: true, CPU: BuildRunnerCPU, Memory: BuildRunnerMemory, EphemeralStorage: BuildRunnerEphemeral, InitGate: true,
		BuildKit: &BuildKit{Image: "moby/buildkit:rootless", CPU: BuildKitCPU, Memory: BuildKitMemory, EphemeralStorage: BuildKitEphemeral},
		Env:      map[string]string{"BUILDKIT_HOST": BuildKitAddr}})
	if err != nil {
		t.Fatal(err)
	}
	s := pod.Spec
	if s.HostNetwork || s.HostPID || s.HostIPC || s.AutomountServiceAccountToken == nil || *s.AutomountServiceAccountToken {
		t.Fatal("rete/PID/IPC dell'host o token del ServiceAccount")
	}
	for _, v := range s.Volumes {
		if v.HostPath != nil {
			t.Fatalf("hostPath %s: nessun socket del runtime", v.Name)
		}
	}
	all := append(append([]corev1.Container{}, s.InitContainers...), s.Containers...)
	if len(s.InitContainers) != 2 || s.InitContainers[0].Name != "gate" || s.InitContainers[1].Name != "buildkitd" ||
		s.InitContainers[1].RestartPolicy == nil || *s.InitContainers[1].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Fatalf("ordine: il gate prima, poi il sidecar nativo di BuildKit: %+v", s.InitContainers)
	}
	for _, c := range all {
		sc := c.SecurityContext
		if sc == nil || (sc.Privileged != nil && *sc.Privileged) || sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
			t.Fatalf("%s: privileged o capability non scartate", c.Name)
		}
		for _, add := range sc.Capabilities.Add {
			if add != "SETUID" && add != "SETGID" {
				t.Fatalf("%s: capability %s non ammessa", c.Name, add)
			}
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Fatalf("%s: runAsNonRoot", c.Name)
		}
		for _, port := range c.Ports {
			if port.HostPort != 0 {
				t.Fatalf("%s: hostPort", c.Name)
			}
		}
	}
	// Il runner e il gate non guadagnano nulla: solo BuildKit ha seccomp e AppArmor Unconfined.
	for _, c := range []corev1.Container{s.InitContainers[0], s.Containers[0]} {
		if *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Fatalf("%s: il runner non deve avere i permessi del sidecar", c.Name)
		}
	}
	// La somma delle richieste sta nella quota del namespace per un posto: 2 CPU, 4 GiB, 20 GiB.
	var cpu, mem, disk int64
	for _, c := range append([]corev1.Container{s.Containers[0]}, s.InitContainers[1]) {
		cpu += c.Resources.Requests.Cpu().MilliValue()
		mem += c.Resources.Requests.Memory().Value()
		disk += c.Resources.Requests.StorageEphemeral().Value()
	}
	if cpu > 2000 || mem > 4<<30 || disk > 20<<30 {
		t.Fatalf("runner + BuildKit oltre la quota di un posto: %dm %d %d", cpu, mem, disk)
	}
	mounted := false
	for _, m := range s.Containers[0].VolumeMounts {
		mounted = mounted || (m.Name == "buildkit-sock" && m.MountPath == BuildKitSocketDir)
	}
	if !mounted {
		t.Fatal("il runner non vede il socket di buildkitd")
	}
}
