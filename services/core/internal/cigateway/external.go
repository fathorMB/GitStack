package cigateway

import (
	"context"
	"errors"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
)

// Runner esterni (GIT-278, ADR 0001 §4). Stesso protocollo runner.v1 dei pod interni:
//
//	Register   token di registrazione monouso → token del runner (gsr_…)
//	Declare    il runner aggiorna etichette e versione (token del runner)
//	FetchTask  con il token del runner: long-poll sul job in coda con runs-on compatibile
//
// Preso il job, tutto il resto (token del job, segreti mascherati, UpdateTask, UpdateLog,
// archivio) è identico al runner integrato: stesso buildTask, stesse credenziali.

type runnerCtxKey struct{}

func runnerFrom(ctx context.Context) (ci.RunnerRow, bool) {
	r, ok := ctx.Value(runnerCtxKey{}).(ci.RunnerRow)
	return r, ok
}

func runnerMsg(r ci.RunnerRow, token string) *runnerv1.Runner {
	return &runnerv1.Runner{Uuid: r.ID.String(), Token: token, Name: r.Name, Version: r.Version, Labels: r.Labels}
}

// Register crea il runner consumando il token di registrazione.
func (r *rpc) Register(ctx context.Context, req *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	g := r.g
	m := req.Msg
	runner, token, err := ci.RegisterRunner(ctx, g.Pool, g.Now(), m.GetToken(), m.GetName(), m.GetVersion(), m.GetLabels())
	switch {
	case errors.Is(err, ci.ErrRegistrationToken):
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token di registrazione non valido"))
	case errors.Is(err, ci.ErrRunnerInput):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("nome o etichette del runner non valide"))
	case errors.Is(err, ci.ErrRunnerName):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("esiste già un runner con questo nome"))
	case err != nil:
		g.Log.Error("ci-gateway: registrazione del runner non riuscita", "err", err)
		return nil, unavailable(errors.New("servizio non disponibile"))
	}
	return connect.NewResponse(&runnerv1.RegisterResponse{Runner: runnerMsg(runner, token)}), nil
}

// Declare aggiorna etichette e versione del runner che chiama.
func (r *rpc) Declare(ctx context.Context, req *connect.Request[runnerv1.DeclareRequest]) (*connect.Response[runnerv1.DeclareResponse], error) {
	g := r.g
	runner, _ := runnerFrom(ctx)
	labels, err := ci.DeclareRunner(ctx, g.Pool, runner.ID, req.Msg.GetVersion(), req.Msg.GetLabels())
	if errors.Is(err, ci.ErrRunnerInput) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("etichette non valide"))
	}
	if err != nil {
		g.Log.Error("ci-gateway: dichiarazione del runner non riuscita", "runner", runner.ID, "err", err)
		return nil, unavailable(errors.New("servizio non disponibile"))
	}
	runner.Labels, runner.Version = labels, req.Msg.GetVersion()
	return connect.NewResponse(&runnerv1.DeclareResponse{Runner: runnerMsg(runner, "")}), nil
}

// fetchExternal è FetchTask di un runner esterno: long-poll fino a PollTimeout.
func (r *rpc) fetchExternal(ctx context.Context, token string) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	g := r.g
	deadline := time.Now().Add(g.PollTimeout) // orologio vero: Now è iniettabile nei test
	for {
		runner, err := ci.ResolveRunner(ctx, g.Pool, g.Now(), token)
		if errors.Is(err, ci.ErrRunnerToken) {
			// Anche un runner revocato mentre aspetta: smette di prendere job.
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token del runner non valido"))
		}
		if err != nil {
			g.Log.Error("ci-gateway: verifica del runner non riuscita", "err", err)
			return nil, unavailable(errors.New("coda non disponibile"))
		}
		job, run, jobToken, ok, err := ci.ClaimExternal(ctx, g.Pool, g.Now(), runner, ci.JobTokenTTL, g.Reporter)
		if errors.Is(err, ci.ErrRunnerToken) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token del runner non valido"))
		}
		if err != nil {
			g.Log.Error("ci-gateway: presa del job non riuscita", "runner", runner.ID, "err", err)
			return nil, unavailable(errors.New("coda non disponibile"))
		}
		if ok {
			task, err := g.buildTask(ctx, job, run, jobToken)
			if err != nil {
				g.Log.Warn("ci-gateway: task non costruibile, il job fallisce", "job", job.ID, "err", err)
				if cerr := ci.CompleteJobReported(context.WithoutCancel(ctx), g.Pool, g.Now(), job.ID, ci.ConclusionFailure, g.Reporter); cerr != nil {
					g.Log.Error("ci-gateway: chiusura del job non riuscita", "job", job.ID, "err", cerr)
				}
				return nil, unavailable(errors.New("task non disponibile"))
			}
			return connect.NewResponse(&runnerv1.FetchTaskResponse{Task: task}), nil
		}
		if !time.Now().Before(deadline) {
			return connect.NewResponse(&runnerv1.FetchTaskResponse{}), nil
		}
		select {
		case <-ctx.Done():
			return connect.NewResponse(&runnerv1.FetchTaskResponse{}), nil
		case <-time.After(g.PollEvery):
		}
	}
}
