package ci

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ZeroSHA è lo sha di un ref che non esiste (creato o eliminato da un push).
const ZeroSHA = "0000000000000000000000000000000000000000"

// Source è ciò che serve da git: i workflow a un commit, i file cambiati fra
// due commit e il messaggio di un commit.
type Source interface {
	// Workflows legge i file workflow di .gitstack/workflows e .github/workflows al commit sha.
	Workflows(ctx context.Context, repoID uuid.UUID, sha string) ([]File, error)
	// Changed elenca i file cambiati fra base e head; un errore = sconosciuti.
	Changed(ctx context.Context, repoID uuid.UUID, base, head string) ([]string, error)
	// CommitMessage è il messaggio completo del commit.
	CommitMessage(ctx context.Context, repoID uuid.UUID, sha string) (string, error)
}

// Engine collega gli eventi alle esecuzioni.
type Engine struct {
	Pool   *pgxpool.Pool
	Source Source
	// Now è l'orologio; nil = time.Now. I test lo sostituiscono.
	Now func() time.Time
	Log *slog.Logger
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// PushRef è un ref aggiornato da un push.
type PushRef struct {
	Ref    string
	Before string
	After  string
	// Message è il messaggio del commit di testa (After); vuoto = da leggere.
	Message         string
	IsDefaultBranch bool
}

// Push è un git.push già letto.
type Push struct {
	RepoID uuid.UUID
	Pusher uuid.UUID
	Refs   []PushRef
}

// IsJobPrincipal dice se id è l'identità del token effimero di un job (CI4):
// i push fatti con quel token non avviano altri workflow.
func (e *Engine) IsJobPrincipal(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := e.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.ci_jobs WHERE token_principal_id = $1)`, id).Scan(&ok)
	return ok, err
}

// OnPush crea le esecuzioni dei workflow `push` per i ref aggiornati e, sul
// branch principale, riallinea gli orari (schedule). Ritorna gli id creati.
func (e *Engine) OnPush(ctx context.Context, p Push) ([]uuid.UUID, error) {
	if job, err := e.IsJobPrincipal(ctx, p.Pusher); err != nil {
		return nil, err
	} else if job {
		return nil, nil // il push del token di un job non avvia nulla (e non tiene vivo il repo)
	}
	now := e.now()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO core.ci_repo_state (repo_id, last_push_at) VALUES ($1, $2)
		ON CONFLICT (repo_id) DO UPDATE SET last_push_at = EXCLUDED.last_push_at`, p.RepoID, now); err != nil {
		return nil, err
	}
	st, err := LoadRepoState(ctx, e.Pool, p.RepoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := e.resumeSchedules(ctx, p.RepoID, now); err != nil {
		return nil, err
	}
	if st.Blocked() != "" {
		return nil, nil
	}
	var created []uuid.UUID
	for _, ref := range p.Refs {
		if ref.After == ZeroSHA || ref.After == "" {
			continue
		}
		_, isBranch := strings.CutPrefix(ref.Ref, "refs/heads/")
		_, isTag := strings.CutPrefix(ref.Ref, "refs/tags/")
		if !isBranch && !isTag {
			continue
		}
		msg := ref.Message
		if msg == "" {
			if msg, err = e.Source.CommitMessage(ctx, p.RepoID, ref.After); err != nil {
				e.log().Warn("ci: messaggio del commit non letto", "repo", p.RepoID, "sha", ref.After, "err", err)
			}
		}
		files, err := e.Source.Workflows(ctx, p.RepoID, ref.After)
		if err != nil {
			return created, fmt.Errorf("lettura dei workflow a %s: %w", ref.After, err)
		}
		wfs := parseAll(files, e.log())
		if ref.IsDefaultBranch {
			if err := e.syncSchedules(ctx, p.RepoID, ref.Ref, ref.After, wfs, now); err != nil {
				return created, err
			}
		}
		if SkipCI(msg) {
			continue
		}
		ev := Event{Kind: EventPush, Ref: ref.Ref, SHA: ref.After, Message: msg}
		var changed []string
		changedLoaded := false
		actor := p.Pusher
		for _, wf := range wfs {
			if wf.Push == nil {
				continue
			}
			if (len(wf.Push.Paths) > 0 || len(wf.Push.PathsIgnore) > 0) && !changedLoaded && isBranch {
				changedLoaded = true
				if ref.Before != ZeroSHA && ref.Before != "" {
					if c, err := e.Source.Changed(ctx, p.RepoID, ref.Before, ref.After); err == nil {
						changed = c
					} else {
						e.log().Warn("ci: file cambiati non letti, i filtri paths non escludono", "repo", p.RepoID, "err", err)
					}
				}
			}
			ev.Changed = changed
			if !wf.Matches(ev) {
				continue
			}
			c, err := e.enqueue(ctx, NewRun{Repo: st, Workflow: wf, Event: EventPush, Ref: ref.Ref, SHA: ref.After,
				GroupKey: "push:" + ref.Ref, ActorID: &actor, Supersede: true})
			if err != nil {
				return created, err
			}
			if !c.Duplicate {
				created = append(created, c.RunID)
			}
		}
	}
	return created, nil
}

// Pull è un evento di una Pull Request (stesso repo: origine e destinazione sono branch del repo).
type Pull struct {
	RepoID       uuid.UUID
	Number       int64
	Action       string // opened, synchronize, reopened
	SourceBranch string
	TargetBranch string
	HeadSHA      string
	Actor        uuid.UUID
}

// OnPull crea le esecuzioni dei workflow `pull_request`.
func (e *Engine) OnPull(ctx context.Context, p Pull) ([]uuid.UUID, error) {
	st, err := LoadRepoState(ctx, e.Pool, p.RepoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if st.Blocked() != "" || p.HeadSHA == "" {
		return nil, nil
	}
	if job, err := e.IsJobPrincipal(ctx, p.Actor); err != nil {
		return nil, err
	} else if job {
		return nil, nil
	}
	msg, err := e.Source.CommitMessage(ctx, p.RepoID, p.HeadSHA)
	if err != nil {
		e.log().Warn("ci: messaggio del commit non letto", "repo", p.RepoID, "sha", p.HeadSHA, "err", err)
	}
	if SkipCI(msg) {
		return nil, nil
	}
	files, err := e.Source.Workflows(ctx, p.RepoID, p.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("lettura dei workflow a %s: %w", p.HeadSHA, err)
	}
	wfs := parseAll(files, e.log())
	ev := Event{Kind: EventPull, Ref: "refs/heads/" + p.SourceBranch, SHA: p.HeadSHA, Message: msg,
		PullNumber: p.Number, PullAction: p.Action, BaseBranch: p.TargetBranch}
	changedLoaded := false
	number, actor := p.Number, p.Actor
	var created []uuid.UUID
	for _, wf := range wfs {
		if wf.PullRequest == nil {
			continue
		}
		f := wf.PullRequest
		if (len(f.Paths) > 0 || len(f.PathsIgnore) > 0) && !changedLoaded {
			changedLoaded = true
			if c, err := e.Source.Changed(ctx, p.RepoID, p.TargetBranch, p.HeadSHA); err == nil {
				ev.Changed = c
			} else {
				e.log().Warn("ci: file cambiati non letti, i filtri paths non escludono", "repo", p.RepoID, "err", err)
			}
		}
		if !wf.Matches(ev) {
			continue
		}
		c, err := e.enqueue(ctx, NewRun{Repo: st, Workflow: wf, Event: EventPull, Ref: fmt.Sprintf("refs/pull/%d/head", p.Number),
			SHA: p.HeadSHA, PullNumber: &number, GroupKey: fmt.Sprintf("pull_request:%d", p.Number), ActorID: &actor, Supersede: true})
		if err != nil {
			return created, err
		}
		if !c.Duplicate {
			created = append(created, c.RunID)
		}
	}
	return created, nil
}

// Errori dell'avvio manuale.
var (
	ErrRepoDisabled     = errors.New("i workflow non girano su questo repo")
	ErrWorkflowNotFound = errors.New("workflow non trovato")
	ErrNotDispatchable  = errors.New("il workflow non ha workflow_dispatch")
	ErrInvalidInputs    = errors.New("input del workflow non validi")
)

// Dispatch è un avvio manuale. Il permesso di scrittura lo controlla chi chiama.
type Dispatch struct {
	RepoID uuid.UUID
	// Workflow è il percorso (.github/workflows/ci.yml) o il solo nome del file (ci.yml).
	Workflow string
	Ref      string // refs/heads/x o refs/tags/x
	SHA      string
	Actor    uuid.UUID
	Inputs   map[string]string
}

// Dispatch avvia un workflow a mano. [skip ci] non vale: l'avvio è esplicito.
func (e *Engine) Dispatch(ctx context.Context, d Dispatch) (uuid.UUID, error) {
	st, err := LoadRepoState(ctx, e.Pool, d.RepoID)
	if err != nil {
		return uuid.Nil, err
	}
	if r := st.Blocked(); r != "" {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrRepoDisabled, r)
	}
	files, err := e.Source.Workflows(ctx, d.RepoID, d.SHA)
	if err != nil {
		return uuid.Nil, err
	}
	var wf *Workflow
	for _, w := range parseAll(files, e.log()) {
		if w.Path == d.Workflow || path.Base(w.Path) == d.Workflow {
			wf = w
			break
		}
	}
	if wf == nil {
		return uuid.Nil, ErrWorkflowNotFound
	}
	if !wf.Dispatch {
		return uuid.Nil, ErrNotDispatchable
	}
	inputs, err := resolveInputs(wf.DispatchInputs, d.Inputs)
	if err != nil {
		return uuid.Nil, err
	}
	actor := d.Actor
	c, err := e.enqueue(ctx, NewRun{Repo: st, Workflow: wf, Event: EventDispatch, Ref: d.Ref, SHA: d.SHA,
		GroupKey: "dispatch:" + uuid.NewString(), ActorID: &actor, Inputs: inputs})
	if err != nil {
		return uuid.Nil, err
	}
	return c.RunID, nil
}

func resolveInputs(decl []DispatchInput, given map[string]string) (map[string]string, error) {
	known := map[string]DispatchInput{}
	for _, d := range decl {
		known[d.Name] = d
	}
	out := map[string]string{}
	for k, v := range given {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("%w: input %q non dichiarato", ErrInvalidInputs, k)
		}
		out[k] = v
	}
	for _, d := range decl {
		if _, ok := out[d.Name]; ok {
			continue
		}
		if d.Required && d.Default == "" {
			return nil, fmt.Errorf("%w: manca l'input obbligatorio %q", ErrInvalidInputs, d.Name)
		}
		if d.Default != "" {
			out[d.Name] = d.Default
		}
	}
	return out, nil
}

// enqueue crea l'esecuzione in una transazione.
func (e *Engine) enqueue(ctx context.Context, n NewRun) (Created, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return Created{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := createRun(ctx, tx, e.now(), n)
	if err != nil {
		return Created{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Created{}, err
	}
	if len(c.Cancelled) > 0 {
		e.log().Info("ci: esecuzioni in coda annullate da un evento più recente", "repo", n.Repo.ID, "workflow", n.Workflow.Path, "n", len(c.Cancelled))
	}
	return c, nil
}

// ListWorkflows legge i workflow del repo al commit sha.
func (e *Engine) ListWorkflows(ctx context.Context, repoID uuid.UUID, sha string) ([]*Workflow, error) {
	files, err := e.Source.Workflows(ctx, repoID, sha)
	if err != nil {
		return nil, err
	}
	return parseAll(files, e.log()), nil
}

// parseAll legge i workflow validi; un file illeggibile si salta con un avviso.
// Lo stesso nome di file in entrambe le cartelle vale una volta sola, con
// precedenza a .gitstack/workflows (WorkflowDirs).
func parseAll(files []File, log *slog.Logger) []*Workflow {
	sort.SliceStable(files, func(i, j int) bool { return dirRank(files[i].Path) < dirRank(files[j].Path) })
	seen := map[string]bool{}
	var out []*Workflow
	for _, f := range files {
		if !IsWorkflowPath(f.Path) || len(f.Content) > MaxWorkflowBytes {
			continue
		}
		base := path.Base(f.Path)
		if seen[base] {
			continue
		}
		wf, err := ParseWorkflow(f.Path, f.Content)
		if err != nil {
			log.Warn("ci: workflow non valido, saltato", "path", f.Path, "err", err)
			continue
		}
		seen[base] = true
		out = append(out, wf)
	}
	return out
}

func dirRank(p string) int {
	for i, d := range WorkflowDirs {
		if strings.HasPrefix(p, d+"/") {
			return i
		}
	}
	return len(WorkflowDirs)
}

// syncSchedules riallinea gli orari del repo ai workflow del branch principale.
func (e *Engine) syncSchedules(ctx context.Context, repoID uuid.UUID, ref, sha string, wfs []*Workflow, now time.Time) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	keep := map[string]bool{}
	for _, wf := range wfs {
		for _, expr := range wf.Schedules {
			cr, err := ParseCron(expr)
			if err != nil {
				continue
			}
			next := cr.Next(now)
			if next.IsZero() {
				continue
			}
			keep[wf.Path+"\x00"+expr] = true
			if _, err := tx.Exec(ctx, `
				INSERT INTO core.ci_schedules (id, repo_id, workflow_path, workflow_name, cron, ref, sha, next_run_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT (repo_id, workflow_path, cron) DO UPDATE SET ref = EXCLUDED.ref, sha = EXCLUDED.sha, workflow_name = EXCLUDED.workflow_name`,
				uuid.New(), repoID, wf.Path, wf.Name, expr, ref, sha, next); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT id, workflow_path, cron FROM core.ci_schedules WHERE repo_id = $1`, repoID)
	if err != nil {
		return err
	}
	var stale []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var wp, cron string
		if err := rows.Scan(&id, &wp, &cron); err != nil {
			rows.Close()
			return err
		}
		if !keep[wp+"\x00"+cron] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(stale) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM core.ci_schedules WHERE id = ANY($1)`, stale); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// resumeSchedules riattiva gli orari sospesi per mancanza di push: il prossimo
// scatto si conta da ora, senza recuperare quelli persi.
func (e *Engine) resumeSchedules(ctx context.Context, repoID uuid.UUID, now time.Time) error {
	rows, err := e.Pool.Query(ctx, `SELECT id, cron FROM core.ci_schedules WHERE repo_id = $1 AND suspended_at IS NOT NULL`, repoID)
	if err != nil {
		return err
	}
	type item struct {
		id   uuid.UUID
		cron string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.cron); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		cr, err := ParseCron(it.cron)
		if err != nil {
			continue
		}
		if _, err := e.Pool.Exec(ctx, `UPDATE core.ci_schedules SET suspended_at = NULL, next_run_at = $2 WHERE id = $1`,
			it.id, cr.Next(now)); err != nil {
			return err
		}
	}
	return nil
}

// Tick fa partire gli orari scaduti. Sicuro con più repliche (SKIP LOCKED):
// ogni scadenza dà al più un'esecuzione (indice su schedule_slot). Uno
// schedule di un repo senza push da più di 60 giorni si sospende; di un repo
// archiviato o in mirror in pull salta lo scatto. Ritorna le esecuzioni create.
func (e *Engine) Tick(ctx context.Context) ([]uuid.UUID, error) {
	now := e.now()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, `
		SELECT id, repo_id, workflow_path, workflow_name, cron, ref, sha, next_run_at
		FROM core.ci_schedules WHERE suspended_at IS NULL AND next_run_at <= $1
		ORDER BY next_run_at LIMIT 100 FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return nil, err
	}
	type due struct {
		id                       uuid.UUID
		repoID                   uuid.UUID
		path, name, cron, ref, s string
		slot                     time.Time
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.repoID, &d.path, &d.name, &d.cron, &d.ref, &d.s, &d.slot); err != nil {
			rows.Close()
			return nil, err
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var created []uuid.UUID
	for _, d := range dues {
		cr, err := ParseCron(d.cron)
		if err != nil {
			continue
		}
		st, err := LoadRepoState(ctx, tx, d.repoID)
		if err != nil {
			return nil, err
		}
		advance := cr.Next(now)
		if st.Dormant(now) {
			if _, err := tx.Exec(ctx, `UPDATE core.ci_schedules SET suspended_at = $2 WHERE id = $1`, d.id, now); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE core.ci_schedules SET next_run_at = $2 WHERE id = $1`, d.id, advance); err != nil {
			return nil, err
		}
		if st.Blocked() != "" {
			continue
		}
		wf, err := e.scheduledWorkflow(ctx, d.repoID, d.s, d.path, d.cron)
		if err != nil {
			return nil, err
		}
		if wf == nil {
			continue
		}
		slot := d.slot
		c, err := createRun(ctx, tx, now, NewRun{Repo: st, Workflow: wf, Event: EventSchedule, Ref: d.ref, SHA: d.s,
			GroupKey: "schedule:" + d.id.String() + ":" + slot.Format(time.RFC3339), ScheduleSlot: &slot})
		if err != nil {
			return nil, err
		}
		if !c.Duplicate {
			created = append(created, c.RunID)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return created, nil
}

// scheduledWorkflow rilegge il workflow di uno schedule al commit registrato;
// nil se non c'è più o non ha più quell'orario.
func (e *Engine) scheduledWorkflow(ctx context.Context, repoID uuid.UUID, sha, wfPath, cron string) (*Workflow, error) {
	files, err := e.Source.Workflows(ctx, repoID, sha)
	if err != nil {
		return nil, fmt.Errorf("lettura dei workflow a %s: %w", sha, err)
	}
	for _, wf := range parseAll(files, e.log()) {
		if wf.Path != wfPath {
			continue
		}
		for _, c := range wf.Schedules {
			if c == cron {
				return wf, nil
			}
		}
	}
	return nil, nil
}
