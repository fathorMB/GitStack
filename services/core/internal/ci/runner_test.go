package ci

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPodForJob_BarriereELimiti(t *testing.T) {
	p, err := PodForJob(RunnerConfig{Image: "registry/runner:fixed", GatewayURL: "http://core:8081", Capacity: 99, ActionMirror: "https://mirror.example/actions"}, JobRow{ID: uuid.New()}, RunRow{ID: uuid.New()})
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
}
