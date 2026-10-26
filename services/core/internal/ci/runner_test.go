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

func TestPodForJob_BuildGitstackBuild(t *testing.T) {
	cfg := RunnerConfig{Image: "runner:1", GatewayURL: "http://core:8081", GateInternalAddr: "core:8080",
		BuildNamespace: "gitstack-ci-build", BuildImage: "moby/buildkit:rootless", RegistryAddr: "gitstack-gateway.ns.svc:8080"}
	job := JobRow{ID: uuid.New(), RunsOn: []string{"ubuntu-latest", BuildLabel}}
	p, err := PodForJob(cfg, job, RunRow{ID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Namespace != "gitstack-ci-build" || p.RunAsUser != BuildUser || p.BuildKit == nil || p.BuildKit.Image != "moby/buildkit:rootless" || !p.InitGate {
		t.Fatalf("pod di build: %+v", p)
	}
	if p.Env["BUILDKIT_HOST"] != BuildKitAddr || p.Env["GITSTACK_REGISTRY"] != "gitstack-gateway.ns.svc:8080" {
		t.Fatalf("env: %v", p.Env)
	}
	// Il biglietto monouso c'e' anche qui, e il pod resta senza token Kubernetes.
	pt, err := PodForJobTicket(cfg, job, RunRow{ID: uuid.New()}, "gjt_x")
	if err != nil || pt.Env["GITSTACK_CI_RUNNER_SECRET"] != "gjt_x" || pt.AutomountServiceAccountToken {
		t.Fatalf("biglietto: %v %+v", err, pt)
	}
	// Senza label il pod resta quello di sempre, anche con la build configurata.
	plain, err := PodForJob(cfg, JobRow{ID: uuid.New(), RunsOn: []string{"ubuntu-latest"}}, RunRow{ID: uuid.New()})
	if err != nil || plain.Namespace != "gitstack-ci-jobs" && plain.Namespace != "" || plain.BuildKit != nil || plain.Env["BUILDKIT_HOST"] != "" {
		t.Fatalf("pod normale: %v %+v", err, plain)
	}
	// Build non configurata: nessun pod (il dispatcher chiude il job in failure).
	if _, err := PodForJob(RunnerConfig{Image: "runner:1"}, job, RunRow{ID: uuid.New()}); err == nil {
		t.Fatal("job di build senza namespace, immagine e registry doveva fallire")
	}
}

func TestRunnerTypeOf_GitstackBuild(t *testing.T) {
	for _, in := range [][]string{{BuildLabel}, {"ubuntu-latest", "GitStack-Build"}} {
		if runnerTypeOf(in) != "internal_pod" || !IsBuildJob(in) {
			t.Errorf("%v: atteso pod interno di build", in)
		}
	}
	if runnerTypeOf([]string{BuildLabel, "self-hosted"}) != "external" {
		t.Error("self-hosted resta esterno anche con la label di build")
	}
	if IsBuildJob([]string{"ubuntu-latest"}) {
		t.Error("ubuntu-latest non e' un job di build")
	}
}
