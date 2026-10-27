package cigateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"google.golang.org/protobuf/types/known/structpb"
)

// rpc implementa runnerv1connect.RunnerServiceHandler (Register e Declare: external.go).
type rpc struct {
	runnerv1connect.UnimplementedRunnerServiceHandler
	g *gateway
}

func resultName(r runnerv1.Result) string {
	switch r {
	case runnerv1.Result_RESULT_SUCCESS:
		return ci.ConclusionSuccess
	case runnerv1.Result_RESULT_FAILURE:
		return ci.ConclusionFailure
	case runnerv1.Result_RESULT_CANCELLED:
		return ci.ConclusionCancelled
	case runnerv1.Result_RESULT_SKIPPED:
		return ci.ConclusionSkipped
	}
	return ""
}

func unavailable(err error) error { return connect.NewError(connect.CodeUnavailable, err) }

// FetchTask è un long-poll: prende il job in coda più adatto (ClaimNextWithToken) o,
// scaduto PollTimeout, risponde senza task. Un job preso è consegnato a un solo runner.
func (r *rpc) FetchTask(ctx context.Context, req *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	g := r.g
	if strings.HasPrefix(bearer(req.Header()), ci.RunnerTokenPrefix) {
		return r.fetchExternal(ctx, bearer(req.Header()))
	}
	job, run, token, err := ci.ConsumeJobTicket(ctx, g.Pool, g.Now(), bearer(req.Header()), ci.JobTokenTTL)
	if errors.Is(err, ci.ErrJobTicket) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("biglietto del job non valido"))
	}
	if err != nil {
		g.Log.Error("ci-gateway: consumo biglietto non riuscito", "err", err)
		return nil, unavailable(errors.New("coda non disponibile"))
	}
	task, err := g.buildTask(ctx, job, run, token)
	if err != nil {
		g.Log.Warn("ci-gateway: task non costruibile, il job fallisce", "job", job.ID, "err", err)
		if cerr := ci.CompleteJobReported(context.WithoutCancel(ctx), g.Pool, g.Now(), job.ID, ci.ConclusionFailure, g.Reporter); cerr != nil {
			g.Log.Error("ci-gateway: chiusura del job non riuscita", "job", job.ID, "err", cerr)
		}
		return nil, unavailable(errors.New("task non disponibile"))
	}
	return connect.NewResponse(&runnerv1.FetchTaskResponse{Task: task}), nil
}

// buildTask compone il task: il file workflow al commit dell'esecuzione, il token del
// job come GITHUB_TOKEN e un contesto `github` minimo. Il job da eseguire è `github.job`.
func (g *gateway) buildTask(ctx context.Context, job ci.JobRow, run ci.RunRow, token string) (*runnerv1.Task, error) {
	if g.Source == nil {
		return nil, errors.New("git non disponibile")
	}
	files, err := g.Source.Workflows(ctx, run.RepoID, run.SHA)
	if err != nil {
		return nil, fmt.Errorf("lettura dei workflow: %w", err)
	}
	var payload []byte
	for _, f := range files {
		if f.Path == run.WorkflowPath {
			payload = f.Content
		}
	}
	if payload == nil {
		return nil, fmt.Errorf("workflow %s non presente a %s", run.WorkflowPath, run.SHA)
	}
	var owner, name string
	if err := g.Pool.QueryRow(ctx, `SELECT owner_name, name FROM core.repositories WHERE resource_id = $1`, run.RepoID).Scan(&owner, &name); err != nil {
		return nil, fmt.Errorf("repo: %w", err)
	}
	cx, err := structpb.NewStruct(taskContext(owner+"/"+name, job, run, token, g.ArchiveURL))
	if err != nil {
		return nil, err
	}
	secrets := map[string]string{}
	vars := map[string]string{}
	if g.Keys != nil {
		env, err := ci.LoadJobEnv(ctx, g.Pool, g.Keys, run.RepoID)
		if err != nil {
			return nil, fmt.Errorf("segreti: %w", err)
		}
		secrets, vars = env.Secrets, env.Vars
	} else if has, err := ci.HasSecrets(ctx, g.Pool, run.RepoID); err != nil || has {
		return nil, errors.New("segreti presenti ma chiave non configurata")
	}
	// Il token del job vince su un segreto omonimo.
	secrets["GITHUB_TOKEN"] = token
	g.maskers.put(job.ID, ci.NewMasker(maskValues(secrets)...))
	return &runnerv1.Task{
		Id:              job.TaskID,
		WorkflowPayload: payload,
		Context:         cx,
		Secrets:         secrets,
		Vars:            vars,
	}, nil
}

// taskContext è il contesto `github` minimo del task. Per un workflow_dispatch contiene anche
// `inputs`: gli input risolti del run, default compresi (li ha riempiti resolveInputs
// all'avvio), che il runner mette in github.event.inputs (GIT-439). Gli altri eventi non
// hanno la chiave.
func taskContext(repository string, job ci.JobRow, run ci.RunRow, token, archiveBase string) map[string]any {
	fields := map[string]any{
		"repository":      repository,
		"sha":             run.SHA,
		"ref":             run.Ref,
		"event_name":      run.Event,
		"run_id":          run.ID.String(),
		"job":             job.Key,
		"workflow":        run.WorkflowName,
		"token":           token,
		"gitstack_job_id": job.ID.String(),
	}
	if archiveBase != "" {
		fields["gitstack_archive_url"] = strings.TrimRight(archiveBase, "/") + ArchivePath
	}
	if run.PullNumber != nil {
		fields["pull_number"] = float64(*run.PullNumber)
	}
	if run.Event == "workflow_dispatch" {
		inputs := make(map[string]any, len(run.Inputs))
		for k, v := range run.Inputs {
			inputs[k] = v
		}
		fields["inputs"] = inputs
	}
	return fields
}

// UpdateTask riporta lo stato dei passi; un esito finale chiude il job (CompleteJob)
// e il run avanza come per CompleteJob.
func (r *rpc) UpdateTask(ctx context.Context, req *connect.Request[runnerv1.UpdateTaskRequest]) (*connect.Response[runnerv1.UpdateTaskResponse], error) {
	g := r.g
	a, _ := authFrom(ctx)
	st := req.Msg.GetState()
	if st == nil || st.GetId() != a.TaskID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("il token non è di questo task"))
	}
	steps := make([]ci.StepReport, 0, len(st.GetSteps()))
	for _, s := range st.GetSteps() {
		sr := ci.StepReport{Number: int(s.GetId()) + 1, Conclusion: resultName(s.GetResult())}
		if s.GetStartedAt().IsValid() {
			t := s.GetStartedAt().AsTime()
			sr.StartedAt = &t
		}
		if s.GetStoppedAt().IsValid() {
			t := s.GetStoppedAt().AsTime()
			sr.StoppedAt = &t
		}
		steps = append(steps, sr)
	}
	if err := ci.ReportSteps(ctx, g.Pool, a.JobID, steps); err != nil {
		g.Log.Error("ci-gateway: passi non registrati", "job", a.JobID, "err", err)
		return nil, unavailable(errors.New("servizio non disponibile"))
	}
	if concl := resultName(st.GetResult()); concl != "" {
		if err := ci.CompleteJobReported(ctx, g.Pool, g.Now(), a.JobID, concl, g.Reporter); err != nil {
			g.Log.Error("ci-gateway: chiusura del job non riuscita", "job", a.JobID, "err", err)
			return nil, unavailable(errors.New("servizio non disponibile"))
		}
		g.maskers.drop(a.JobID)
	}
	return connect.NewResponse(&runnerv1.UpdateTaskResponse{State: &runnerv1.TaskState{Id: a.TaskID, Result: st.GetResult()}}), nil
}

// UpdateLog scrive le righe del job (idempotente per indice) e ritorna l'indice successivo atteso.
func (r *rpc) UpdateLog(ctx context.Context, req *connect.Request[runnerv1.UpdateLogRequest]) (*connect.Response[runnerv1.UpdateLogResponse], error) {
	g := r.g
	a, _ := authFrom(ctx)
	if req.Msg.GetTaskId() != a.TaskID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("il token non è di questo task"))
	}
	if g.Logs == nil {
		return nil, unavailable(errors.New("log non configurati"))
	}
	rows := req.Msg.GetRows()
	if len(rows) > maxLogRowsPerCall {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("troppe righe in una chiamata"))
	}
	lines := make([]ci.LogLine, 0, len(rows))
	for _, row := range rows {
		l := ci.LogLine{Content: row.GetContent()}
		if row.GetTime().IsValid() {
			l.Time = row.GetTime().AsTime()
		}
		lines = append(lines, l)
	}
	mask, err := g.maskerFor(ctx, a, bearer(req.Header()))
	if err != nil {
		g.Log.Error("ci-gateway: segreti del job non caricati", "job", a.JobID, "err", err)
		return nil, unavailable(errors.New("servizio non disponibile"))
	}
	ack, err := g.Logs.AppendMasked(ctx, g.Pool, g.Now(), a.JobID, req.Msg.GetIndex(), lines, mask)
	if err != nil {
		g.Log.Error("ci-gateway: log non scritti", "job", a.JobID, "err", err)
		return nil, unavailable(errors.New("servizio non disponibile"))
	}
	return connect.NewResponse(&runnerv1.UpdateLogResponse{AckIndex: ack}), nil
}
