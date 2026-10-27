package cigateway

import (
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"google.golang.org/protobuf/types/known/structpb"
)

// GIT-439: il contesto del task di un workflow_dispatch porta gli input risolti del run
// (default compresi); un run per push non ha la chiave `inputs`.
func TestTaskContext_InputDelRun(t *testing.T) {
	dispatch := ci.RunRow{
		Event: "workflow_dispatch", Ref: "refs/heads/main", SHA: "0123456789012345678901234567890123456789",
		Inputs: map[string]string{"versione": "0.0.1", "rompi": "no"},
	}
	cx := taskContext("alice/app", ci.JobRow{Key: "build"}, dispatch, "gsj_x", "http://gw/")
	// Il contesto deve poter diventare una struct protobuf, com'e' nel task.
	if _, err := structpb.NewStruct(cx); err != nil {
		t.Fatal(err)
	}
	in, ok := cx["inputs"].(map[string]any)
	if !ok || len(in) != 2 || in["versione"] != "0.0.1" || in["rompi"] != "no" {
		t.Fatalf("inputs: %#v", cx["inputs"])
	}
	if cx["sha"] != dispatch.SHA || cx["ref"] != "refs/heads/main" || cx["gitstack_archive_url"] != "http://gw"+ArchivePath {
		t.Errorf("contesto: %v", cx)
	}

	// Un dispatch senza input dichiarati ha comunque la mappa, vuota.
	empty := taskContext("alice/app", ci.JobRow{Key: "build"}, ci.RunRow{Event: "workflow_dispatch"}, "t", "")
	if in, ok := empty["inputs"].(map[string]any); !ok || len(in) != 0 {
		t.Errorf("dispatch senza input: %#v", empty["inputs"])
	}

	push := ci.RunRow{Event: "push", Ref: "refs/heads/main", SHA: dispatch.SHA, Inputs: map[string]string{"x": "y"}}
	if _, has := taskContext("alice/app", ci.JobRow{Key: "build"}, push, "t", "")["inputs"]; has {
		t.Error("un run push non deve avere inputs")
	}
}
