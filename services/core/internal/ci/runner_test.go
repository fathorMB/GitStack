package ci

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPodForJob_BarriereELimiti(t *testing.T) {
	p, err := PodForJob(RunnerConfig{Image: "registry/runner:fixed", GatewayURL: "http://core:8081", GateInternalAddr: "core:8080", Capacity: 99, ActionMirror: "https://mirror.example/actions"}, JobRow{ID: uuid.New()}, RunRow{ID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Namespace != "gitstack-ci-jobs" || p.PriorityClass != "gitstack-ci-job" || p.ActiveDeadlineSeconds != int64((2*time.Hour)/time.Second) {
		t.Fatalf("pod: %+v", p)
	}
	if p.AutomountServiceAccountToken || p.AllowPrivilegeEscalation || !p.ReadOnlyRootFilesystem || !p.DropAllCapabilities || p.RunAsUser != 10001 {
		t.Fatalf("barriere mancanti: %+v", p)
	}
	if p.CPU != MaxCPU || p.Memory != MaxMemory || p.EphemeralStorage != MaxEphemeral {
		t.Fatalf("limiti: %+v", p)
	}
	if p.Env["GITSTACK_CI_ACTION_MIRROR"] != "https://mirror.example/actions" {
		t.Fatal("mirror non passato")
	}
	if p.Env["GITSTACK_CI_GATEWAY_URL"] != "http://core:8081" {
		t.Fatal("gateway non passato")
	}
	if p.Env["GITSTACK_CI_GATE_INTERNAL_ADDR"] != "core:8080" {
		t.Fatal("endpoint interno del gate non passato")
	}
}

func TestPodForJob_ProxyECAAziendale(t *testing.T) {
	env := ProxyEnvFrom(func(k string) string {
		return map[string]string{"HTTPS_PROXY": "http://proxy:3128", "NO_PROXY": ".svc", "GITSTACK_CI_RUNNER": "evil"}[k]
	})
	if len(env) != 2 {
		t.Fatalf("ProxyEnvFrom = %v", env)
	}
	cfg := RunnerConfig{Image: "r:1", GatewayURL: "http://core:8081", ProxyEnv: env, CAConfigMap: "gitstack-corporate-ca"}
	cfg.ProxyEnv["GITSTACK_CI_RUNNER"] = "evil"
	p, err := PodForJob(cfg, JobRow{ID: uuid.New()}, RunRow{ID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["HTTPS_PROXY"] != "http://proxy:3128" || p.Env["NO_PROXY"] != ".svc" || p.Env["GITSTACK_CI_RUNNER"] != "act-host" {
		t.Fatalf("env = %v", p.Env)
	}
	if p.Env["SSL_CERT_DIR"] != CAMountDir || p.CAConfigMap != "gitstack-corporate-ca" {
		t.Fatalf("CA non montata: %v %q", p.Env, p.CAConfigMap)
	}
	k, err := corePod(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range k.Spec.Volumes {
		if v.Name == "corporate-ca" && v.ConfigMap != nil && v.ConfigMap.Name == "gitstack-corporate-ca" {
			found = true
		}
	}
	if !found || len(k.Spec.Containers[0].VolumeMounts) != 3 {
		t.Fatalf("volume della CA assente: %+v", k.Spec)
	}
	plain, _ := PodForJob(RunnerConfig{Image: "r:1"}, JobRow{ID: uuid.New()}, RunRow{ID: uuid.New()})
	if _, ok := plain.Env["HTTPS_PROXY"]; ok || plain.CAConfigMap != "" {
		t.Fatalf("senza proxy né CA non deve aggiungere nulla: %v", plain.Env)
	}
}
