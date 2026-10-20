package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// CI v2-a (GIT-276, CI3): dai push, dalle PR, dagli avvii manuali e dagli
// orari alle esecuzioni (internal/ci). Qui il collegamento con git, i
// consumer degli eventi e le rotte; le regole stanno in internal/ci.

// ciSystemCaller è l'identità con cui core legge i workflow da git: non è un
// utente, e non vede nulla che l'API non esporrebbe (legge solo le cartelle dei workflow).
var ciSystemCaller = trust.Identity{UserID: uuid.Nil.String(), Username: "system:ci"}

// gitCISource implementa ci.Source sopra le letture del servizio git.
type gitCISource struct {
	rd gitclient.Reader
}

func isGitNotFound(err error) bool {
	var ae *gitclient.APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusBadRequest)
}

func (g gitCISource) Workflows(ctx context.Context, repoID uuid.UUID, sha string) ([]ci.File, error) {
	var out []ci.File
	for _, dir := range ci.WorkflowDirs {
		raw, err := g.rd.ReadJSON(ctx, ciSystemCaller, repoID, "tree", url.Values{"ref": {sha}, "path": {dir}})
		if isGitNotFound(err) {
			continue // la cartella non c'è a questo commit
		}
		if err != nil {
			return nil, err
		}
		var tree openapi.Tree
		if err := json.Unmarshal(raw, &tree); err != nil {
			return nil, err
		}
		for _, e := range tree.Entries {
			if e.Type != openapi.File || !ci.IsWorkflowPath(e.Path) {
				continue
			}
			if e.Size != nil && *e.Size > ci.MaxWorkflowBytes {
				continue
			}
			raw, err := g.rd.ReadJSON(ctx, ciSystemCaller, repoID, "contents", url.Values{"ref": {sha}, "path": {e.Path}})
			if isGitNotFound(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var fc openapi.FileContent
			if err := json.Unmarshal(raw, &fc); err != nil {
				return nil, err
			}
			if fc.Content == nil || fc.Binary {
				continue
			}
			out = append(out, ci.File{Path: e.Path, Content: []byte(*fc.Content)})
		}
	}
	return out, nil
}

func (g gitCISource) Changed(ctx context.Context, repoID uuid.UUID, base, head string) ([]string, error) {
	raw, err := g.rd.ReadJSON(ctx, ciSystemCaller, repoID, "compare", url.Values{"base": {base}, "head": {head}})
	if err != nil {
		return nil, err
	}
	var c pullCompare
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.Truncated || c.ListOnly {
		// L'elenco può non essere completo: i filtri paths non devono escludere.
		if c.Truncated {
			return nil, fmt.Errorf("elenco dei file cambiati troncato")
		}
	}
	files := make([]string, 0, len(c.Files)*2)
	for _, f := range c.Files {
		files = append(files, f.Path)
		if f.OldPath != nil {
			files = append(files, *f.OldPath)
		}
	}
	return files, nil
}

func (g gitCISource) CommitMessage(ctx context.Context, repoID uuid.UUID, sha string) (string, error) {
	raw, err := g.rd.ReadJSON(ctx, ciSystemCaller, repoID, "commits/"+url.PathEscape(sha), nil)
	if err != nil {
		return "", err
	}
	var c struct {
		Message string `json:"message"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	if c.Message != "" {
		return c.Message, nil
	}
	return c.Subject, nil
}

// ciEngine costruisce il motore sulle letture di git; nil se git non le offre.
func (s *apiServer) ciEngine() *ci.Engine {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		return nil
	}
	return &ci.Engine{Pool: s.pool, Source: gitCISource{rd: rd}, Now: s.now, ActorLevel: s.ciActorLevel, Reporter: ciReporter{s: s}}
}

// NewCIPushHandler è il gitpush.Handler delle esecuzioni dei workflow (durable
// core-ci-git): un push su un branch o un tag avvia i workflow `push`.
func NewCIPushHandler(pool *pgxpool.Pool, publisher events.Publisher, opts ...Option) func(ctx context.Context, env pkgevents.Envelope, p pkggitpush.Payload) error {
	s := newAPIServer(pool, publisher, opts...)
	return s.handleCIPush
}

func (s *apiServer) handleCIPush(ctx context.Context, _ pkgevents.Envelope, p pkggitpush.Payload) error {
	eng := s.ciEngine()
	if eng == nil {
		return nil
	}
	pusher, err := uuid.Parse(p.Pusher.ID)
	if err != nil {
		return nil
	}
	owner, name, ok := strings.Cut(p.Repo.FullName, "/")
	if !ok {
		return nil
	}
	repo, err := s.resources.GetRepoByName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	in := ci.Push{RepoID: repo.ID, Pusher: pusher}
	for _, r := range p.Refs {
		pr := ci.PushRef{Ref: r.Ref, Before: r.Before, After: r.After, IsDefaultBranch: r.IsDefaultBranch}
		if len(r.Commits) > 0 {
			pr.Message = r.Commits[0].Message
		}
		in.Refs = append(in.Refs, pr)
	}
	_, err = eng.OnPush(ctx, in)
	return err
}

// ciPullEvent avvia i workflow `pull_request` dopo che la PR è stata scritta.
// È best-effort: una CI che non risponde non deve far fallire la PR.
func (s *apiServer) ciPullEvent(ctx context.Context, ia issueAccess, number int64, action string) {
	eng := s.ciEngine()
	if eng == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	p, err := getPR(ctx, s.pool, ia.repo.ID, number, false)
	if err != nil || p.HeadSha == nil {
		return
	}
	if _, err := eng.OnPull(ctx, ci.Pull{RepoID: ia.repo.ID, Number: number, Action: action, SourceBranch: p.SourceBranch,
		TargetBranch: p.TargetBranch, HeadSHA: *p.HeadSha, Actor: ia.userID}); err != nil {
		slog.Default().Warn("ci: esecuzioni della PR non create", "repo", ia.repo.ID, "pull", number, "err", err)
	}
}

// RunCISchedules fa partire gli orari scaduti ogni interval, finché ctx non finisce.
func RunCISchedules(ctx context.Context, pool *pgxpool.Pool, publisher events.Publisher, interval time.Duration, log *slog.Logger, opts ...Option) {
	s := newAPIServer(pool, publisher, opts...)
	eng := s.ciEngine()
	if eng == nil {
		return
	}
	eng.Log = log
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := eng.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Warn("ci: scansione degli orari non riuscita", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- rotte ----

func (s *apiServer) ciAccess(w http.ResponseWriter, r *http.Request, owner, name string) (issueAccess, *ci.Engine, bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return ia, nil, false
	}
	eng := s.ciEngine()
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Letture del codice non disponibili.")
		return ia, nil, false
	}
	return ia, eng, true
}

func workflowID(p string) string { return path.Base(p) }

// ListWorkflows implementa GET /repos/{owner}/{repo}/workflows.
func (s *apiServer) ListWorkflows(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, eng, ok := s.ciAccess(w, r, owner, name)
	if !ok {
		return
	}
	sha, err := s.ciResolve(r.Context(), ia.repo.ID, ia.repo.DefaultBranch)
	if err != nil {
		if isGitNotFound(err) { // repo senza commit
			writeJSON(w, http.StatusOK, openapi.WorkflowList{Items: []openapi.Workflow{}})
			return
		}
		gitFailure(w, err)
		return
	}
	wfs, err := eng.ListWorkflows(r.Context(), ia.repo.ID, sha)
	if err != nil {
		gitFailure(w, err)
		return
	}
	state := openapi.WorkflowState("active")
	if ia.repo.ArchivedAt != nil {
		state = "disabled"
	}
	items := make([]openapi.Workflow, 0, len(wfs))
	for _, wf := range wfs {
		items = append(items, openapi.Workflow{Id: workflowID(wf.Path), Name: wf.Name, Path: wf.Path, State: state})
	}
	writeJSON(w, http.StatusOK, openapi.WorkflowList{Items: items})
}

// ciResolve traduce un branch, un tag o uno sha nello sha del commit.
func (s *apiServer) ciResolve(ctx context.Context, repoID uuid.UUID, rev string) (string, error) {
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		return "", gitclient.ErrUnavailable
	}
	raw, err := rd.ReadJSON(ctx, ciSystemCaller, repoID, "commits/"+url.PathEscape(rev)+"/resolve", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Sha string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Sha) != 40 {
		return "", fmt.Errorf("risposta di git non valida")
	}
	return out.Sha, nil
}

// DispatchWorkflow implementa POST /repos/{owner}/{repo}/workflows/{workflow}/dispatches.
func (s *apiServer) DispatchWorkflow(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, workflow openapi.WorkflowParam) {
	ia, eng, ok := s.ciAccess(w, r, owner, name)
	if !ok {
		return
	}
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Avviare un workflow richiede write sul repository.")
		return
	}
	var in openapi.WorkflowDispatchInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if strings.TrimSpace(in.Ref) == "" {
		writeError(w, http.StatusBadRequest, "invalid_ref", "Indicare un ref: un branch, un tag o uno sha.")
		return
	}
	ctx := r.Context()
	ref, sha, err := s.ciResolveRef(ctx, ia.repo.ID, strings.TrimSpace(in.Ref))
	if err != nil {
		if isGitNotFound(err) {
			writeError(w, http.StatusBadRequest, "invalid_ref", "Il ref indicato non esiste.")
			return
		}
		gitFailure(w, err)
		return
	}
	inputs := map[string]string{}
	if in.Inputs != nil {
		inputs = *in.Inputs
	}
	id, err := eng.Dispatch(ctx, ci.Dispatch{RepoID: ia.repo.ID, Workflow: string(workflow), Ref: ref, SHA: sha, Actor: ia.userID, Inputs: inputs})
	switch {
	case errors.Is(err, ci.ErrRepoDisabled):
		writeError(w, http.StatusConflict, "ci_disabled", "I workflow non girano su questo repository (archiviato o mirror in pull).")
		return
	case errors.Is(err, ci.ErrWorkflowNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Workflow non trovato.")
		return
	case errors.Is(err, ci.ErrNotDispatchable), errors.Is(err, ci.ErrInvalidInputs):
		writeError(w, http.StatusBadRequest, "invalid_dispatch", err.Error())
		return
	case err != nil:
		slog.Default().Warn("ci: avvio manuale non riuscito", "repo", ia.repo.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Impossibile accodare l'esecuzione.")
		return
	}
	run, err := ci.GetRun(ctx, s.pool, ia.repo.ID, id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Esecuzione accodata ma non leggibile.")
		return
	}
	writeJSON(w, http.StatusAccepted, runAPI(run))
}

// ciResolveRef risolve il ref di un avvio manuale: un branch o un tag (nome
// breve o completo) oppure uno sha. Ritorna il nome completo e lo sha.
func (s *apiServer) ciResolveRef(ctx context.Context, repoID uuid.UUID, in string) (string, string, error) {
	candidates := []string{in}
	if !strings.HasPrefix(in, "refs/") {
		candidates = []string{"refs/heads/" + in, "refs/tags/" + in, in}
	}
	var last error
	for _, c := range candidates {
		sha, err := s.ciResolve(ctx, repoID, c)
		if err == nil {
			if c == in && !strings.HasPrefix(in, "refs/") {
				return in, sha, nil // uno sha
			}
			return c, sha, nil
		}
		last = err
		if !isGitNotFound(err) {
			return "", "", err
		}
	}
	return "", "", last
}

func runAPI(r ci.RunRow) openapi.WorkflowRun {
	out := openapi.WorkflowRun{
		Id: openapi_types.UUID(r.ID), WorkflowId: workflowID(r.WorkflowPath), Status: r.Status, Conclusion: r.Conclusion,
		Event: openapi.WorkflowRunEvent(r.Event), Ref: r.Ref, Sha: r.SHA, CreatedAt: r.CreatedAt,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, Jobs: make([]openapi.WorkflowJob, 0, len(r.Jobs)),
	}
	for _, j := range r.Jobs {
		out.Jobs = append(out.Jobs, jobAPI(j))
	}
	return out
}

func jobAPI(j ci.JobRow) openapi.WorkflowJob {
	st := j.Status
	if st == ci.StatusWaiting {
		st = ci.StatusQueued // il contratto conosce solo queued, in_progress, completed, cancelled
	}
	capped := j.Permissions[ci.PermCappedByActor] == "true"
	return openapi.WorkflowJob{
		Id: openapi_types.UUID(j.ID), Name: j.Name, Status: st, Conclusion: j.Conclusion,
		RunnerType: openapi.WorkflowJobRunnerType(j.RunnerType), RunnerName: j.RunnerName,
		StartedAt: j.StartedAt, CompletedAt: j.CompletedAt,
		Permissions: openapi.JobTokenPermissions{
			CappedByActor: &capped,
			Contents:      openapi.JobTokenPermissionsContents(j.Permissions["contents"]),
			Packages:      openapi.JobTokenPermissionsPackages(j.Permissions["packages"]),
			Actions:       openapi.JobTokenPermissionsActions(j.Permissions["actions"]),
		},
	}
}

// ListWorkflowRuns implementa GET /repos/{owner}/{repo}/actions/runs.
func (s *apiServer) ListWorkflowRuns(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	runs, err := ci.ListRuns(r.Context(), s.pool, ia.repo.ID, 30)
	if err != nil {
		slog.Default().Warn("ci: elenco delle esecuzioni non riuscito", "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Esecuzioni non disponibili.")
		return
	}
	items := make([]openapi.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		items = append(items, runAPI(run))
	}
	writeJSON(w, http.StatusOK, openapi.WorkflowRunList{Items: items})
}

// GetWorkflowRun implementa GET /repos/{owner}/{repo}/actions/runs/{runId}.
func (s *apiServer) GetWorkflowRun(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.RunIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	run, err := ci.GetRun(r.Context(), s.pool, ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Esecuzione non trovata.")
		return
	}
	if err != nil {
		slog.Default().Warn("ci: lettura dell'esecuzione non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Esecuzione non disponibile.")
		return
	}
	writeJSON(w, http.StatusOK, runAPI(run))
}

// CancelWorkflowRun implementa POST /repos/{owner}/{repo}/actions/runs/{runId}/cancel (GIT-314).
func (s *apiServer) CancelWorkflowRun(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.RunIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Annullare un'esecuzione richiede write sul repository.")
		return
	}
	run, err := ci.CancelRunReported(r.Context(), s.pool, s.now(), ia.repo.ID, uuid.UUID(id), ciReporter{s: s})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "Esecuzione non trovata.")
		return
	case errors.Is(err, ci.ErrRunConcluded):
		writeError(w, http.StatusConflict, "run_concluded", "L'esecuzione è già conclusa: non si annulla.")
		return
	case err != nil:
		slog.Default().Warn("ci: annullamento dell'esecuzione non riuscito", "repo", ia.repo.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Impossibile annullare l'esecuzione.")
		return
	}
	writeJSON(w, http.StatusAccepted, runAPI(run))
}

// RerunWorkflowRun implementa POST /repos/{owner}/{repo}/actions/runs/{runId}/rerun (GIT-314):
// riesegue un'esecuzione conclusa in una esecuzione nuova.
func (s *apiServer) RerunWorkflowRun(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.RunIdParam) {
	ia, eng, ok := s.ciAccess(w, r, owner, name)
	if !ok {
		return
	}
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Rieseguire un'esecuzione richiede write sul repository.")
		return
	}
	ctx := r.Context()
	newID, err := eng.Rerun(ctx, ia.repo.ID, uuid.UUID(id), ia.userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "Esecuzione non trovata.")
		return
	case errors.Is(err, ci.ErrRunNotConcluded):
		writeError(w, http.StatusConflict, "run_not_concluded", "L'esecuzione non è ancora conclusa: non si riesegue.")
		return
	case errors.Is(err, ci.ErrRepoDisabled):
		writeError(w, http.StatusConflict, "ci_disabled", "I workflow non girano su questo repository (archiviato o mirror in pull).")
		return
	case errors.Is(err, ci.ErrWorkflowGone):
		writeError(w, http.StatusConflict, "workflow_gone", "Il workflow non c'è più al commit dell'esecuzione.")
		return
	case err != nil:
		if isGitNotFound(err) {
			writeError(w, http.StatusConflict, "workflow_gone", "Il commit dell'esecuzione non è più leggibile.")
			return
		}
		gitFailure(w, err)
		return
	}
	run, err := ci.GetRun(ctx, s.pool, ia.repo.ID, newID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Esecuzione accodata ma non leggibile.")
		return
	}
	writeJSON(w, http.StatusAccepted, runAPI(run))
}

// GetWorkflowJob implementa GET /repos/{owner}/{repo}/actions/jobs/{jobId} (GIT-314).
// Un job di un altro repo è 404, come un repo non leggibile.
func (s *apiServer) GetWorkflowJob(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.JobIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	job, err := ci.GetJob(r.Context(), s.pool, ia.repo.ID, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Job non trovato.")
		return
	}
	if err != nil {
		slog.Default().Warn("ci: lettura del job non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "ci_unavailable", "Job non disponibile.")
		return
	}
	writeJSON(w, http.StatusOK, jobAPI(job))
}

// ciActorLevel dice se chi avvia un'esecuzione legge e scrive sul repo (tetto del token del job, CI4).
func (s *apiServer) ciActorLevel(ctx context.Context, actor, repoID uuid.UUID) (bool, bool, error) {
	if s.repoIdentity == nil {
		return false, false, errors.New("identity non configurata")
	}
	write, err := s.repoIdentity.HasRole(ctx, actor, repoID, "write")
	if err != nil {
		return false, false, err
	}
	if write {
		return true, true, nil
	}
	read, err := s.repoIdentity.HasRole(ctx, actor, repoID, "read")
	return read, false, err
}
