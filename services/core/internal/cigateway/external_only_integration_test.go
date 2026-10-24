//go:build integration

package cigateway_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
)

// TestExternalOnly_RifiutaIBigliettiDeiPod (GIT-329): sul gateway esposto fuori dal cluster
// FetchTask vale solo con il token di un runner registrato. Il biglietto di un pod interno
// riceve Unauthenticated e non si consuma: sul gateway interno prende ancora il job.
func TestExternalOnly_RifiutaIBigliettiDeiPod(t *testing.T) {
	e := newEnv(t)
	e.queue("app", "a")

	ext := httptest.NewServer(cigateway.New(cigateway.Config{
		Pool: e.pool, Source: e.src, Reader: e.rd, Logs: e.logs, ExternalOnly: true,
		ArchiveURL: "https://ci.example.test", Now: e.now, PollTimeout: 400 * time.Millisecond, PollEvery: 25 * time.Millisecond,
	}))
	t.Cleanup(ext.Close)
	extClient := runnerv1connect.NewRunnerServiceClient(ext.Client(), ext.URL)

	_, _, ticket, ok, err := ci.ClaimNextWithTicketReported(context.Background(), e.pool, e.now(), 3, "internal-pod", nil)
	if err != nil || !ok {
		t.Fatalf("biglietto: ok=%v err=%v", ok, err)
	}
	for _, tok := range []string{ticket, "", "sbagliato", ci.JobTokenPrefix + "xxx"} {
		_, err := extClient.FetchTask(context.Background(), authed(&runnerv1.FetchTaskRequest{}, tok))
		if code(err) != connect.CodeUnauthenticated {
			t.Errorf("gateway esterno, credenziale %q: %v", tok, err)
		}
	}
	// Il biglietto non e' stato consumato dal rifiuto.
	res, err := e.client.FetchTask(context.Background(), authed(&runnerv1.FetchTaskRequest{}, ticket))
	if err != nil || res.Msg.GetTask() == nil {
		t.Fatalf("il biglietto doveva valere ancora sul gateway interno: %v", err)
	}

	// Un runner registrato passa dal gateway esterno e il task punta all'indirizzo pubblico.
	e.queueWF("ext", "b", externalWF)
	rt, err := e.register("r1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	res, err = extClient.FetchTask(context.Background(), authed(&runnerv1.FetchTaskRequest{}, rt))
	if err != nil || res.Msg.GetTask() == nil {
		t.Fatalf("runner registrato sul gateway esterno: %v %v", res, err)
	}
	if got := res.Msg.GetTask().GetContext().AsMap()["gitstack_archive_url"]; got != "https://ci.example.test"+cigateway.ArchivePath {
		t.Errorf("gitstack_archive_url = %v", got)
	}
}

// TestExternal_TokenDelRunnerPerUpdateLog (GIT-329): il runner di Gitea usa il proprio token
// (gsr_) anche per UpdateLog/UpdateTask, ma solo per il job che sta eseguendo: un altro
// runner, o un runner senza job in corso, riceve Unauthenticated.
func TestExternal_TokenDelRunnerPerUpdateLog(t *testing.T) {
	e := newEnv(t)
	e.queueWF("ext", "b", externalWF)
	r1, err := e.register("r1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.register("r2", "gpu")
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.fetch(r1)
	if err != nil || task == nil {
		t.Fatalf("fetch: %v %v", task, err)
	}
	send := func(tok string) error {
		_, err := e.client.UpdateLog(context.Background(), authed(&runnerv1.UpdateLogRequest{
			TaskId: task.GetId(), Index: 0, Rows: []*runnerv1.LogRow{{Content: "ciao"}}}, tok))
		return err
	}
	if err := send(r1); err != nil {
		t.Errorf("il runner del job deve poter scrivere il log: %v", err)
	}
	if err := send(r2); code(err) != connect.CodeUnauthenticated {
		t.Errorf("un altro runner: %v", err)
	}
	if err := send("gsq_inventato"); code(err) != connect.CodeUnauthenticated {
		t.Errorf("biglietto di un pod: %v", err)
	}
	_, err = e.client.UpdateTask(context.Background(), authed(&runnerv1.UpdateTaskRequest{
		State: &runnerv1.TaskState{Id: task.GetId() + 99, Result: runnerv1.Result_RESULT_SUCCESS}}, r1))
	if code(err) != connect.CodePermissionDenied {
		t.Errorf("task di un altro job: %v", err)
	}
}
