package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Risultati della CI (v2-a, GIT-281, CI6). Il Reporter riceve, nella transazione
// di ogni cambio di stato di un job o di un'esecuzione (ci.Change), e scrive
// nella stessa transazione:
//   - lo stato del commit con contesto «<workflow> / <job>», con la stessa
//     tabella di PR9 (una riga per contesto): i contesti obbligatori della PR
//     (PR4) lo leggono, così un job rosso blocca il merge;
//   - commit_status.created, e pull_request.checks_failed se il contesto è
//     obbligatorio (emitCommitStatusRaw);
//   - workflow_run.* e workflow_job.* per i webhook;
//   - workflow_run.completed per le notifiche (chi ha avviato l'esecuzione,
//     quando fallisce e quando torna verde).
// Se la transazione fallisce, fallisce anche il cambio di stato: nessuno stato
// resta indietro rispetto al job.

// ciReporter è il ci.Reporter sull'apiServer: stessa identità e stesse letture
// di core, nessun controllo di permesso (agisce per conto del sistema CI).
type ciReporter struct{ s *apiServer }

// NewCIReporter è il Reporter della CI per chi lo costruisce fuori dall'API (la
// porta del ci-gateway, i test). opts sono le stesse opzioni del router.
func NewCIReporter(pool *pgxpool.Pool, opts ...Option) ci.Reporter {
	return ciReporter{s: newAPIServer(pool, nil, opts...)}
}

// Changed implementa ci.Reporter.
func (r ciReporter) Changed(ctx context.Context, tx pgx.Tx, c ci.Change) error {
	repo, err := store.RepoInTx(ctx, tx, c.Run.RepoID)
	if err != nil {
		return fmt.Errorf("repo dell'esecuzione: %w", err)
	}
	creator := uuid.Nil
	if c.Run.ActorID != nil {
		creator = *c.Run.ActorID
	}
	ia := issueAccess{repo: repo, userID: creator}
	var actor *domainevents.User
	if c.Run.ActorID != nil {
		actor = actorOf(ctx, *c.Run.ActorID)
	}
	runRaw, err := json.Marshal(runAPI(c.Run))
	if err != nil {
		return err
	}
	for _, j := range c.Jobs {
		if err := r.s.reportJob(ctx, tx, ia, actor, c.Run, j, runRaw); err != nil {
			return err
		}
	}
	if !c.RunChanged {
		return nil
	}
	prev := ""
	if c.Run.Status == ci.StatusCompleted {
		if prev, err = previousConclusion(ctx, tx, c.Run); err != nil {
			return err
		}
	}
	return outbox.Enqueue(ctx, tx, domainevents.DomainWorkflowRun+"."+ciAction(c.Run.Status), domainevents.Version,
		domainevents.WorkflowRunPayload{Repo: repoSnapshot(repo), Actor: actor, Run: runRaw, Previous: prev})
}

// reportJob scrive lo stato del commit del job e gli eventi del job.
func (s *apiServer) reportJob(ctx context.Context, tx pgx.Tx, ia issueAccess, actor *domainevents.User, run ci.RunRow,
	j ci.JobRow, runRaw json.RawMessage) error {
	state, desc := ciJobStatus(j)
	x, prev, err := writeCommitStatus(ctx, tx, ia.repo.ID, ia.userID, run.SHA, ciStatusContext(run, j), state, desc, "", nil, nil, false)
	if err != nil {
		return err
	}
	// Il creatore con nome e tipo se identity risponde; altrimenti solo l'id:
	// un'identity giù non deve bloccare la chiusura del job.
	creator := openapi.IssueUser{Id: openapi_types.UUID(ia.userID), Username: "", Kind: openapi.IssueUserKindHuman}
	if dir, derr := s.resolveUsers(ctx, []uuid.UUID{ia.userID}); derr == nil {
		if u, ok := dir[ia.userID]; ok {
			creator = u
		}
	} else {
		slog.Default().Warn("ci: nome del creatore dello stato non letto", "err", derr)
	}
	d := desc
	raw, err := json.Marshal(openapi.CommitStatus{Id: openapi_types.UUID(x.ID), Sha: x.Sha, Context: x.Context,
		State: openapi.CommitStatusState(x.State), Description: &d, Creator: creator, CreatedAt: x.UpdatedAt})
	if err != nil {
		return err
	}
	if err := s.emitCommitStatusRaw(ctx, tx, ia, x, raw, prev, []string{}); err != nil {
		return err
	}
	jobRaw, err := json.Marshal(jobAPI(j))
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, tx, domainevents.DomainWorkflowJob+"."+ciAction(j.Status), domainevents.Version,
		domainevents.WorkflowJobPayload{Repo: repoSnapshot(ia.repo), Actor: actor, Run: runRaw, Job: jobRaw})
}

// ciAction è l'azione dei webhook per uno stato di esecuzione o di job: in coda
// (anche in attesa dei job da cui dipende) è queued, il resto ha lo stesso nome.
func ciAction(status string) string {
	if status == ci.StatusWaiting {
		return "queued"
	}
	return status
}

// ciJobStatus è lo stato del commit di un job e la sua descrizione. Il job
// saltato (un job da cui dipendeva non è riuscito) e l'annullato sono error:
// bloccano il merge come un rosso, e non restano mai pending.
func ciJobStatus(j ci.JobRow) (state, desc string) {
	switch j.Status {
	case ci.StatusWaiting, ci.StatusQueued:
		return "pending", "In coda"
	case ci.StatusInProgress:
		return "pending", "In corso"
	}
	concl := ""
	if j.Conclusion != nil {
		concl = *j.Conclusion
	}
	switch concl {
	case ci.ConclusionSuccess:
		return "success", "Riuscito"
	case ci.ConclusionFailure:
		return "failure", "Fallito"
	case ci.ConclusionTimedOut:
		return "failure", "Scaduto"
	case ci.ConclusionCancelled:
		return "error", "Annullato"
	case ci.ConclusionSkipped:
		return "error", "Saltato: un job richiesto non è riuscito"
	}
	return "error", "Concluso senza esito"
}

// ciStatusContext è il contesto dello stato di un job: «<workflow> / <job>»
// (GIT-281), con i nomi del workflow e del job o, se mancano, le chiavi. Al
// massimo 255 caratteri, come un contesto scritto a mano.
func ciStatusContext(run ci.RunRow, j ci.JobRow) string {
	wf := run.WorkflowName
	if wf == "" {
		wf = run.WorkflowPath
	}
	name := j.Name
	if name == "" {
		name = j.Key
	}
	out := wf + " / " + name
	if r := []rune(out); len(r) > maxStatusContextRunes {
		out = string(r[:maxStatusContextRunes])
	}
	return out
}

// previousConclusion è la conclusione dell'ultima esecuzione completata dello
// stesso workflow sullo stesso ref, prima di run; vuota se non c'è. Serve a
// dire se la nuova esecuzione torna al verde.
func previousConclusion(ctx context.Context, tx pgx.Tx, run ci.RunRow) (string, error) {
	var concl *string
	err := tx.QueryRow(ctx, `SELECT conclusion FROM core.ci_runs
		WHERE repo_id = $1 AND workflow_path = $2 AND ref = $3 AND status = 'completed' AND id <> $4
			AND completed_at < COALESCE($5::timestamptz, now())
		ORDER BY completed_at DESC LIMIT 1`, run.RepoID, run.WorkflowPath, run.Ref, run.ID, run.CompletedAt).Scan(&concl)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("esecuzione precedente: %w", err)
	}
	if concl == nil {
		return "", nil
	}
	return *concl, nil
}
