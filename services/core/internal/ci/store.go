package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stati e conclusioni (come nel contratto).
const (
	StatusWaiting    = "waiting"
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"

	ConclusionSuccess   = "success"
	ConclusionFailure   = "failure"
	ConclusionCancelled = "cancelled"
	ConclusionSkipped   = "skipped"
	ConclusionTimedOut  = "timed_out"
)

// DefaultCapacity è il numero di job che girano insieme (CI7: 1–3); la
// configurazione può abbassarlo o alzarlo fino a 3.
const DefaultCapacity = 3

// OwnerLimit è il massimo di job in corso per owner: la metà dei posti, almeno uno.
func OwnerLimit(capacity int) int {
	if l := capacity / 2; l > 1 {
		return l
	}
	return 1
}

// RunRow è un'esecuzione.
type RunRow struct {
	ID           uuid.UUID
	RepoID       uuid.UUID
	WorkflowPath string
	WorkflowName string
	Event        string
	Ref          string
	SHA          string
	PullNumber   *int64
	GroupKey     string
	ActorID      *uuid.UUID
	Inputs       map[string]string
	Status       string
	Conclusion   *string
	ScheduleSlot *time.Time
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	Jobs         []JobRow
}

// JobRow è un job di un'esecuzione.
type JobRow struct {
	ID          uuid.UUID
	RunID       uuid.UUID
	Seq         int
	Key         string
	Name        string
	Needs       []string
	RunnerType  string
	RunnerName  *string
	Permissions map[string]string
	Status      string
	Conclusion  *string
	QueuedAt    *time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	// TaskID è il numero del task nel protocollo runner.v1 (mai riusato).
	TaskID int64
	// CancelRequestedAt: annullamento richiesto mentre il job era in corso; il
	// job resta in_progress finché il runner non lo chiude (CompleteJob, cancelled).
	CancelRequestedAt *time.Time
	// RunsOn sono le etichette di runs-on normalizzate (la label gitstack-build sceglie il pod di build).
	RunsOn []string
}

const runColumns = `id, repo_id, workflow_path, workflow_name, event, ref, sha, pull_number, group_key, actor_id, inputs,
	status, conclusion, schedule_slot, created_at, started_at, completed_at`

const jobColumns = `id, run_id, seq, job_key, name, needs, runner_type, runner_name, permissions, status, conclusion,
	queued_at, started_at, completed_at, task_id, cancel_requested_at, runs_on`

func scanRun(row pgx.Row) (RunRow, error) {
	var r RunRow
	var inputs []byte
	err := row.Scan(&r.ID, &r.RepoID, &r.WorkflowPath, &r.WorkflowName, &r.Event, &r.Ref, &r.SHA, &r.PullNumber, &r.GroupKey,
		&r.ActorID, &inputs, &r.Status, &r.Conclusion, &r.ScheduleSlot, &r.CreatedAt, &r.StartedAt, &r.CompletedAt)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(inputs, &r.Inputs)
	return r, nil
}

func scanJob(row pgx.Row) (JobRow, error) {
	var j JobRow
	var perm []byte
	err := row.Scan(&j.ID, &j.RunID, &j.Seq, &j.Key, &j.Name, &j.Needs, &j.RunnerType, &j.RunnerName, &perm, &j.Status,
		&j.Conclusion, &j.QueuedAt, &j.StartedAt, &j.CompletedAt, &j.TaskID, &j.CancelRequestedAt, &j.RunsOn)
	if err != nil {
		return j, err
	}
	_ = json.Unmarshal(perm, &j.Permissions)
	return j, nil
}

// NewRun sono i dati per creare un'esecuzione.
type NewRun struct {
	Repo         RepoState
	Workflow     *Workflow
	Event        string
	Ref          string
	SHA          string
	PullNumber   *int64
	GroupKey     string
	ActorID      *uuid.UUID
	Inputs       map[string]string
	ScheduleSlot *time.Time
	// Supersede: un'esecuzione ancora in coda dello stesso workflow e gruppo
	// (branch o PR) viene annullata dalla nuova.
	Supersede bool
	// ActorCap è il tetto di permessi di chi ha avviato l'esecuzione (none, read, write);
	// vuoto = nessun attore (schedule): vale solo il tetto del repo.
	ActorCap string
}

// Created è l'esito di una creazione.
type Created struct {
	RunID uuid.UUID
	// Cancelled sono le esecuzioni in coda superate da questa.
	Cancelled []uuid.UUID
	// Duplicate: la scadenza di schedule c'era già, nulla è stato creato.
	Duplicate bool
	// JobIDs sono i job dell'esecuzione nuova; CancelledJobIDs quelli annullati
	// con le esecuzioni superate (GIT-281: serve al Reporter).
	JobIDs          []uuid.UUID
	CancelledJobIDs []uuid.UUID
}

// LoadRepoState legge lo stato del repo che conta per CI3.
func LoadRepoState(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, repoID uuid.UUID) (RepoState, error) {
	var (
		st                  RepoState
		id, ownerID         uuid.UUID
		archived, deleted   *time.Time
		pullMirror, runMirr bool
		lastPush            *time.Time
		mirrorBy            *uuid.UUID
	)
	err := q.QueryRow(ctx, `
		SELECT r.resource_id, r.owner_type, r.owner_id, r.archived_at, r.deleted_at, r.created_at,
		       COALESCE(m.state <> 'stopped', false), COALESCE(s.run_on_pull_mirror, false), s.last_push_at, s.run_on_pull_mirror_by
		FROM core.repositories r
		LEFT JOIN core.repo_pull_mirrors m ON m.repo_id = r.resource_id
		LEFT JOIN core.ci_repo_state s ON s.repo_id = r.resource_id
		WHERE r.resource_id = $1`, repoID).
		Scan(&id, &st.OwnerType, &ownerID, &archived, &deleted, &st.CreatedAt, &pullMirror, &runMirr, &lastPush, &mirrorBy)
	if err != nil {
		return RepoState{}, err
	}
	st.ID, st.OwnerID = id.String(), ownerID.String()
	st.Archived, st.Deleted = archived != nil, deleted != nil
	st.PullMirror, st.RunOnPullMirror = pullMirror, runMirr
	if mirrorBy != nil {
		st.MirrorCIBy = mirrorBy.String()
	}
	if lastPush != nil {
		st.LastPushAt = *lastPush
	}
	return st, nil
}

var errNoRepo = errors.New("repo non trovato")

// createRun inserisce esecuzione e job nella transazione tx, annullando prima le
// esecuzioni in coda superate.
func createRun(ctx context.Context, tx pgx.Tx, now time.Time, n NewRun) (Created, error) {
	repoID, err := uuid.Parse(n.Repo.ID)
	if err != nil {
		return Created{}, errNoRepo
	}
	ownerID, _ := uuid.Parse(n.Repo.OwnerID)
	var out Created
	if n.Supersede {
		// Serializza le creazioni dello stesso gruppo: due push ravvicinati
		// non devono lasciare due esecuzioni in coda.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			repoID.String()+"|"+n.Workflow.Path+"|"+n.GroupKey); err != nil {
			return Created{}, err
		}
		rows, err := tx.Query(ctx, `
			UPDATE core.ci_runs SET status = 'cancelled', conclusion = 'cancelled', completed_at = $4
			WHERE repo_id = $1 AND workflow_path = $2 AND group_key = $3 AND status = 'queued'
			RETURNING id`, repoID, n.Workflow.Path, n.GroupKey, now)
		if err != nil {
			return Created{}, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return Created{}, err
			}
			out.Cancelled = append(out.Cancelled, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Created{}, err
		}
		if len(out.Cancelled) > 0 {
			jrows, err := tx.Query(ctx, `UPDATE core.ci_jobs SET status = 'cancelled', conclusion = 'cancelled', completed_at = $2
				WHERE run_id = ANY($1) AND status IN ('waiting', 'queued') RETURNING id`, out.Cancelled, now)
			if err != nil {
				return Created{}, err
			}
			for jrows.Next() {
				var id uuid.UUID
				if err := jrows.Scan(&id); err != nil {
					jrows.Close()
					return Created{}, err
				}
				out.CancelledJobIDs = append(out.CancelledJobIDs, id)
			}
			jrows.Close()
			if err := jrows.Err(); err != nil {
				return Created{}, err
			}
		}
	}
	inputs, _ := json.Marshal(nonNil(n.Inputs))
	out.RunID = uuid.New()
	tag, err := tx.Exec(ctx, `
		INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, workflow_name, event, ref, sha,
			pull_number, group_key, actor_id, inputs, schedule_slot, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (repo_id, workflow_path, schedule_slot) WHERE event = 'schedule' DO NOTHING`,
		out.RunID, repoID, n.Repo.OwnerType, ownerID, n.Workflow.Path, n.Workflow.Name, n.Event, n.Ref, n.SHA,
		n.PullNumber, n.GroupKey, n.ActorID, inputs, n.ScheduleSlot, now)
	if err != nil {
		return Created{}, err
	}
	if tag.RowsAffected() == 0 {
		return Created{Duplicate: true}, nil
	}
	repoDefaults, err := LoadRepoTokenDefaults(ctx, tx, repoID)
	if err != nil {
		return Created{}, err
	}
	for i, j := range n.Workflow.Jobs {
		status := StatusQueued
		var queuedAt *time.Time
		if len(j.Needs) > 0 {
			status = StatusWaiting
		} else {
			queuedAt = &now
		}
		caps := CapPermissions(j.Permissions, repoDefaults, n.ActorCap)
		stored := caps.Permissions
		if caps.CappedByActor {
			stored[PermCappedByActor] = "true"
		}
		perm, _ := json.Marshal(stored)
		jobID := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, needs, runner_type, runs_on, permissions, status, queued_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			jobID, out.RunID, i, j.Key, j.Name, nonNilStrings(j.Needs), runnerTypeOf(j.RunsOn), NormalizeLabels(j.RunsOn), perm, status, queuedAt); err != nil {
			return Created{}, err
		}
		out.JobIDs = append(out.JobIDs, jobID)
	}
	return out, nil
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// runnerTypeOf: i job con etichette di un host vero (self-hosted, Windows, macOS)
// o con un'etichetta che il pod interno non offre (tutto ciò che non è ubuntu-*)
// vanno ai runner esterni (ADR 0001, GIT-278); gli altri a un pod interno. La label
// gitstack-build (BuildLabel) resta interna: sceglie il pod di build (ADR 0001 §6, GIT-371).
func runnerTypeOf(runsOn []string) string {
	for _, l := range NormalizeLabels(runsOn) {
		if !strings.HasPrefix(l, "ubuntu-") && !strings.Contains(l, "${{") && l != BuildLabel {
			return "external"
		}
	}
	return "internal_pod"
}

// NormalizeLabels porta le etichette in minuscolo, toglie il suffisso «:schema» del
// protocollo di Gitea (es. «linux:host»), gli spazi, i vuoti e i doppioni.
func NormalizeLabels(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if i := strings.IndexByte(l, ':'); i >= 0 {
			l = l[:i]
		}
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// ClaimNext prende il job in coda più vecchio che rispetta i limiti: al massimo
// capacity job in corso in tutto e OwnerLimit(capacity) per owner (CI7). Fra i
// candidati vince l'owner con meno job in corso, poi il job in coda da più
// tempo: un owner con molti job non fa aspettare gli altri. false se non c'è nulla.
func ClaimNext(ctx context.Context, pool *pgxpool.Pool, now time.Time, capacity int, runner string) (JobRow, RunRow, bool, error) {
	job, run, _, _, ok, err := claimNext(ctx, pool, now, capacity, runner, 0, 0, nil)
	return job, run, ok, err
}

// ClaimNextWithToken è ClaimNext che, nella stessa transazione, emette il token
// del job (ADR 0001 §3): valido per tokenTTL, solo per quel job. Ritorna il
// token in chiaro, che non si può rileggere.
func ClaimNextWithToken(ctx context.Context, pool *pgxpool.Pool, now time.Time, capacity int, runner string, tokenTTL time.Duration) (JobRow, RunRow, string, bool, error) {
	job, run, token, _, ok, err := claimNext(ctx, pool, now, capacity, runner, tokenTTL, 0, nil)
	return job, run, token, ok, err
}

// ClaimNextReported è ClaimNextWithToken con un Reporter (GIT-281): il job preso
// passa in_progress e l'esecuzione, se era in coda, anche, nella stessa transazione.
func ClaimNextReported(ctx context.Context, pool *pgxpool.Pool, now time.Time, capacity int, runner string, tokenTTL time.Duration, rep Reporter) (JobRow, RunRow, string, bool, error) {
	job, run, token, _, ok, err := claimNext(ctx, pool, now, capacity, runner, tokenTTL, 0, rep)
	return job, run, token, ok, err
}

// ClaimNextWithTicketReported prende un job per il dispatcher interno e crea
// nello stesso commit il biglietto monouso che verra' iniettato nel suo pod.
func ClaimNextWithTicketReported(ctx context.Context, pool *pgxpool.Pool, now time.Time, capacity int, runner string, rep Reporter) (JobRow, RunRow, string, bool, error) {
	job, run, _, ticket, ok, err := claimNext(ctx, pool, now, capacity, runner, 0, JobTicketTTL, rep)
	return job, run, ticket, ok, err
}

func claimNext(ctx context.Context, pool *pgxpool.Pool, now time.Time, capacity int, runner string, tokenTTL, ticketTTL time.Duration, rep Reporter) (JobRow, RunRow, string, string, bool, error) {
	if capacity < 1 {
		capacity = DefaultCapacity
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return JobRow{}, RunRow{}, "", "", false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Il conteggio dei posti deve essere coerente fra repliche: un lock di coda.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('core.ci_queue', 0))`); err != nil {
		return JobRow{}, RunRow{}, "", "", false, err
	}
	var running int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM core.ci_jobs WHERE status = 'in_progress' AND runner_type = 'internal_pod'`).Scan(&running); err != nil {
		return JobRow{}, RunRow{}, "", "", false, err
	}
	if running >= capacity {
		return JobRow{}, RunRow{}, "", "", false, nil
	}
	var jobID, runID uuid.UUID
	err = tx.QueryRow(ctx, `
		WITH busy AS (
			SELECT r.owner_type, r.owner_id, count(*) AS n
			FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id
			WHERE j.status = 'in_progress' AND j.runner_type = 'internal_pod' GROUP BY r.owner_type, r.owner_id)
		SELECT j.id, j.run_id
		FROM core.ci_jobs j
		JOIN core.ci_runs r ON r.id = j.run_id
		LEFT JOIN busy b ON b.owner_type = r.owner_type AND b.owner_id = r.owner_id
		WHERE j.status = 'queued' AND j.runner_type = 'internal_pod' AND r.status IN ('queued', 'in_progress') AND COALESCE(b.n, 0) < $1
		ORDER BY COALESCE(b.n, 0), j.queued_at, j.seq
		LIMIT 1
		FOR UPDATE OF j SKIP LOCKED`, OwnerLimit(capacity)).Scan(&jobID, &runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRow{}, RunRow{}, "", "", false, nil
	}
	if err != nil {
		return JobRow{}, RunRow{}, "", "", false, err
	}
	job, run, token, ticket, err := startClaimed(ctx, tx, now, jobID, runID, runner, nil, tokenTTL, ticketTTL, rep)
	if err != nil {
		return JobRow{}, RunRow{}, "", "", false, err
	}
	return job, run, token, ticket, true, nil
}

// startClaimed porta in corso il job scelto, emette token e biglietto richiesti, riporta i
// cambi e conferma la transazione. runnerID è il runner esterno che lo prende (nil per i pod interni).
func startClaimed(ctx context.Context, tx pgx.Tx, now time.Time, jobID, runID uuid.UUID, runner string, runnerID *uuid.UUID, tokenTTL, ticketTTL time.Duration, rep Reporter) (JobRow, RunRow, string, string, error) {
	if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET status = 'in_progress', started_at = $2, runner_name = NULLIF($3, ''), runner_id = $4
		WHERE id = $1`, jobID, now, runner, runnerID); err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	// Con questa UPDATE l'esecuzione passa in_progress solo se era in coda: il
	// report la include in quel caso (started_at = now, appena scritto).
	runTag, err := tx.Exec(ctx, `UPDATE core.ci_runs SET status = 'in_progress', started_at = COALESCE(started_at, $2)
		WHERE id = $1 AND status = 'queued'`, runID, now)
	if err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	var token string
	if tokenTTL > 0 {
		if token, err = issueJobToken(ctx, tx, now, jobID, tokenTTL); err != nil {
			return JobRow{}, RunRow{}, "", "", err
		}
	}
	var ticket string
	if ticketTTL > 0 {
		if ticket, err = issueJobTicket(ctx, tx, now, jobID, ticketTTL); err != nil {
			return JobRow{}, RunRow{}, "", "", err
		}
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE id = $1`, jobID))
	if err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM core.ci_runs WHERE id = $1`, runID))
	if err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	var runIDs []uuid.UUID
	if runTag.RowsAffected() > 0 {
		runIDs = append(runIDs, runID)
	}
	if err := report(ctx, tx, rep, runIDs, []uuid.UUID{jobID}); err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return JobRow{}, RunRow{}, "", "", err
	}
	return job, run, token, ticket, nil
}

// CompleteJob chiude un job in corso. Con successo promuove i job che
// aspettavano; con un fallimento i dipendenti sono saltati; a job finiti
// chiude anche l'esecuzione.
func CompleteJob(ctx context.Context, pool *pgxpool.Pool, now time.Time, jobID uuid.UUID, conclusion string) error {
	return CompleteJobReported(ctx, pool, now, jobID, conclusion, nil)
}

// CompleteJobReported è CompleteJob con un Reporter (GIT-281): i job cambiati
// (il chiuso, i promossi in coda, i saltati) e l'esecuzione, se si è chiusa, sono
// riportati nella stessa transazione.
func CompleteJobReported(ctx context.Context, pool *pgxpool.Pool, now time.Time, jobID uuid.UUID, conclusion string, rep Reporter) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT run_id FROM core.ci_jobs WHERE id = $1 AND status = 'in_progress' FOR UPDATE`, jobID).Scan(&runID); err != nil {
		return fmt.Errorf("job %s non in corso: %w", jobID, err)
	}
	status := StatusCompleted
	if conclusion == ConclusionCancelled {
		status = StatusCancelled
	}
	if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET status = $2, conclusion = $3, completed_at = $4 WHERE id = $1`,
		jobID, status, conclusion, now); err != nil {
		return err
	}
	touched := []uuid.UUID{jobID}
	var touchedRuns []uuid.UUID
	// Il token del job vale finché il job gira (ADR 0001 §3).
	if err := revokeJobToken(ctx, tx, now, jobID); err != nil {
		return err
	}
	// Serializza la promozione dei job della stessa esecuzione. Un run già
	// annullato (CancelRun) resta annullato anche quando l'ultimo job in corso chiude.
	var curRunStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM core.ci_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&curRunStatus); err != nil {
		return err
	}
	runCancelled := curRunStatus == StatusCancelled
	rows, err := tx.Query(ctx, `SELECT job_key, status, conclusion, needs, id FROM core.ci_jobs WHERE run_id = $1 ORDER BY seq`, runID)
	if err != nil {
		return err
	}
	type jstate struct {
		key, status string
		concl       *string
		needs       []string
		id          uuid.UUID
	}
	var all []jstate
	for rows.Next() {
		var s jstate
		if err := rows.Scan(&s.key, &s.status, &s.concl, &s.needs, &s.id); err != nil {
			rows.Close()
			return err
		}
		all = append(all, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	byKey := map[string]*jstate{}
	for i := range all {
		byKey[all[i].key] = &all[i]
	}
	// Più passate: un job saltato può far saltare a catena i suoi dipendenti.
	for changed := true; changed; {
		changed = false
		for i := range all {
			s := &all[i]
			if s.status != StatusWaiting {
				continue
			}
			ready, failed := true, false
			for _, n := range s.needs {
				d := byKey[n]
				if d.status != StatusCompleted && d.status != StatusCancelled {
					ready = false
					continue
				}
				if d.concl == nil || *d.concl != ConclusionSuccess {
					failed = true
				}
			}
			switch {
			case failed:
				// Un needs non è riuscito: il job è saltato.
				sk := ConclusionSkipped
				if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET status = 'completed', conclusion = 'skipped', completed_at = $2 WHERE id = $1`, s.id, now); err != nil {
					return err
				}
				s.status, s.concl, changed = StatusCompleted, &sk, true
			case ready:
				if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET status = 'queued', queued_at = $2 WHERE id = $1`, s.id, now); err != nil {
					return err
				}
				s.status, changed = StatusQueued, true
			default:
				continue
			}
			touched = append(touched, s.id)
		}
	}
	done, anyFail, anyCancel := true, false, false
	for _, s := range all {
		if s.status != StatusCompleted && s.status != StatusCancelled {
			done = false
			continue
		}
		if s.concl != nil {
			switch *s.concl {
			case ConclusionFailure, ConclusionTimedOut:
				anyFail = true
			case ConclusionCancelled:
				anyCancel = true
			}
		}
	}
	if done && !runCancelled {
		runStatus, runConcl := StatusCompleted, ConclusionSuccess
		switch {
		case anyFail:
			runConcl = ConclusionFailure
		case anyCancel:
			runStatus, runConcl = StatusCancelled, ConclusionCancelled
		}
		if _, err := tx.Exec(ctx, `UPDATE core.ci_runs SET status = $2, conclusion = $3, completed_at = $4 WHERE id = $1`,
			runID, runStatus, runConcl, now); err != nil {
			return err
		}
		touchedRuns = append(touchedRuns, runID)
	}
	if err := report(ctx, tx, rep, touchedRuns, touched); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ErrRunConcluded: l'esecuzione è già completata o annullata.
var ErrRunConcluded = errors.New("esecuzione già conclusa")

// CancelRun annulla un'esecuzione del repo (GIT-314). Un'esecuzione in coda o in
// corso va in cancelled: i job in attesa o in coda sono annullati subito; i job
// in corso ricevono una richiesta di annullamento (cancel_requested_at) e restano
// in_progress finché il runner non li chiude con CompleteJob(…, cancelled).
// pgx.ErrNoRows se l'esecuzione non è del repo; ErrRunConcluded se è già conclusa.
func CancelRun(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID, runID uuid.UUID) (RunRow, error) {
	return CancelRunReported(ctx, pool, now, repoID, runID, nil)
}

// CancelRunReported è CancelRun con un Reporter (GIT-281): l'esecuzione e i job
// annullati subito sono riportati nella stessa transazione.
func CancelRunReported(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID, runID uuid.UUID, rep Reporter) (RunRow, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return RunRow{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM core.ci_runs WHERE id = $1 AND repo_id = $2 FOR UPDATE`, runID, repoID).Scan(&status); err != nil {
		return RunRow{}, err
	}
	if status == StatusCompleted || status == StatusCancelled {
		return RunRow{}, ErrRunConcluded
	}
	if _, err := tx.Exec(ctx, `UPDATE core.ci_runs SET status = 'cancelled', conclusion = 'cancelled', completed_at = $2 WHERE id = $1`,
		runID, now); err != nil {
		return RunRow{}, err
	}
	jrows, err := tx.Query(ctx, `UPDATE core.ci_jobs SET status = 'cancelled', conclusion = 'cancelled', completed_at = $2
		WHERE run_id = $1 AND status IN ('waiting', 'queued') RETURNING id`, runID, now)
	if err != nil {
		return RunRow{}, err
	}
	var cancelled []uuid.UUID
	for jrows.Next() {
		var id uuid.UUID
		if err := jrows.Scan(&id); err != nil {
			jrows.Close()
			return RunRow{}, err
		}
		cancelled = append(cancelled, id)
	}
	jrows.Close()
	if err := jrows.Err(); err != nil {
		return RunRow{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET cancel_requested_at = $2
		WHERE run_id = $1 AND status = 'in_progress' AND cancel_requested_at IS NULL`, runID, now); err != nil {
		return RunRow{}, err
	}
	if err := report(ctx, tx, rep, []uuid.UUID{runID}, cancelled); err != nil {
		return RunRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RunRow{}, err
	}
	return GetRun(ctx, pool, repoID, runID)
}

// GetJob legge un job del repo; pgx.ErrNoRows se il job non appartiene al repo.
func GetJob(ctx context.Context, pool *pgxpool.Pool, repoID, jobID uuid.UUID) (JobRow, error) {
	return scanJob(pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs
		WHERE id = $1 AND run_id IN (SELECT id FROM core.ci_runs WHERE repo_id = $2)`, jobID, repoID))
}

// GetRun legge un'esecuzione di un repo con i suoi job.
func GetRun(ctx context.Context, pool *pgxpool.Pool, repoID, runID uuid.UUID) (RunRow, error) {
	r, err := scanRun(pool.QueryRow(ctx, `SELECT `+runColumns+` FROM core.ci_runs WHERE id = $1 AND repo_id = $2`, runID, repoID))
	if err != nil {
		return RunRow{}, err
	}
	jobs, err := loadJobs(ctx, pool, []uuid.UUID{r.ID})
	if err != nil {
		return RunRow{}, err
	}
	r.Jobs = jobs[r.ID]
	return r, nil
}

// ListRuns elenca le esecuzioni di un repo, le più recenti per prime.
func ListRuns(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID, limit int) ([]RunRow, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := pool.Query(ctx, `SELECT `+runColumns+` FROM core.ci_runs WHERE repo_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`, repoID, limit)
	if err != nil {
		return nil, err
	}
	var out []RunRow
	var ids []uuid.UUID
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	jobs, err := loadJobs(ctx, pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Jobs = jobs[out[i].ID]
	}
	return out, nil
}

func loadJobs(ctx context.Context, pool *pgxpool.Pool, runIDs []uuid.UUID) (map[uuid.UUID][]JobRow, error) {
	out := map[uuid.UUID][]JobRow{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE run_id = ANY($1) ORDER BY run_id, seq`, runIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out[j.RunID] = append(out[j.RunID], j)
	}
	return out, rows.Err()
}

// RequeueJob rimette in coda un job preso ma senza pod (rifiuto transitorio di Kubernetes,
// GIT-452): torna queued con il suo queued_at, il biglietto emesso alla presa è eliminato
// (ne serve uno nuovo alla prossima presa, la chiave è il job) e il cambio è riportato.
// false se il job non era più in_progress (per esempio annullato nel frattempo).
func RequeueJob(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID, rep Reporter) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE core.ci_jobs SET status = 'queued', started_at = NULL, runner_name = NULL, runner_id = NULL
		WHERE id = $1 AND status = 'in_progress' AND runner_type = 'internal_pod'`, jobID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.ci_job_tickets WHERE job_id = $1`, jobID); err != nil {
		return false, err
	}
	if err := report(ctx, tx, rep, nil, []uuid.UUID{jobID}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
