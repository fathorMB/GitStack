package mirrors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (e *Engine) now() time.Time {
	e.init()
	return e.Now()
}

// Requeue mette in coda i mirror che rispondono a where, una clausola su
// core.repo_mirrors con i suoi argomenti numerati da $3 ($1 e $2 sono
// riservati): pending, push_seq avanzato e next_attempt_at = now. Un mirror
// in esecuzione (syncing) resta tale: il suo worker, finito, vede il push
// nuovo e lo riaccoda. resetAttempts azzera i tentativi falliti (richiesta a
// mano). Ritorna quanti mirror ha toccato.
func Requeue(ctx context.Context, pool *pgxpool.Pool, now time.Time, resetAttempts bool, where string, args ...any) (int64, error) {
	tag, err := pool.Exec(ctx, `UPDATE core.repo_mirrors SET pending = true, push_seq = push_seq + 1, next_attempt_at = $1,
			attempts = CASE WHEN $2::boolean THEN 0 ELSE attempts END,
			state = CASE WHEN state IN ('in_sync', 'diverged', 'error') THEN 'pending' ELSE state END,
			updated_at = $1
		WHERE `+where, append([]any{now, resetAttempts}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("messa in coda dei mirror: %w", err)
	}
	return tag.RowsAffected(), nil
}

type job struct {
	id, repoID uuid.UUID
	url, user  string
	ct, nonce  []byte
	keyID      string
	attempts   int
	seq        int64
}

// ProcessDue prende in carico i mirror dovuti (con un affitto) e li esegue, al
// più Workers in parallelo. Ritorna quanti ne ha eseguiti. Un errore del
// database ferma il giro: i mirror presi restano affittati fino alla
// scadenza dell'affitto e poi si riprendono.
func (e *Engine) ProcessDue(ctx context.Context) (int, error) {
	e.init()
	now := e.Now()
	rows, err := e.Pool.Query(ctx, `UPDATE core.repo_mirrors m SET state = 'syncing', lease_until = $2, last_attempt_at = $1, updated_at = $1
		WHERE m.id IN (
			SELECT id FROM core.repo_mirrors
			WHERE enabled AND pending AND state <> 'diverged' AND next_attempt_at <= $1
			  AND (lease_until IS NULL OR lease_until < $1)
			ORDER BY next_attempt_at, id LIMIT $3
			FOR UPDATE SKIP LOCKED)
		RETURNING m.id, m.repo_id, m.url, m.username, m.token_ciphertext, m.token_nonce, m.token_key_id, m.attempts, m.push_seq`,
		now, now.Add(e.Lease), e.Batch)
	if err != nil {
		return 0, err
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.repoID, &j.url, &j.user, &j.ct, &j.nonce, &j.keyID, &j.attempts, &j.seq); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, e.Workers)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := e.execute(ctx, j); err != nil && ctx.Err() == nil {
				e.Log.Warn("mirror: esito non registrato, si riprende dopo l'affitto", "mirror", j.id, "err", err)
			}
		}()
	}
	wg.Wait()
	return len(jobs), nil
}

// outcome è il risultato di un'esecuzione, pronto da scrivere.
type outcome struct {
	kind   string // Outcome*
	err    string
	refs   []gitclient.MirrorRef
	pushed map[string]string
	retry  bool
	start  time.Time
	dur    time.Duration
}

func (e *Engine) execute(ctx context.Context, j job) error {
	oc := e.push(ctx, j)
	return e.finish(ctx, j, oc)
}

// push fa il lavoro: controlli, egress, chiamata a git.
func (e *Engine) push(ctx context.Context, j job) (oc outcome) {
	oc.start = e.now()
	defer func() { oc.dur = e.now().Sub(oc.start) }()
	fail := func(retry bool, format string, args ...any) outcome {
		oc.kind, oc.retry, oc.err = OutcomeError, retry, fmt.Sprintf(format, args...)
		return oc
	}

	var branch string
	var deleted *time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT default_branch, deleted_at FROM core.repositories WHERE resource_id = $1`, j.repoID).Scan(&branch, &deleted); err != nil {
		return fail(true, "lettura del repo non riuscita")
	}
	if deleted != nil {
		return fail(true, "il repo è nel cestino")
	}
	token, err := e.Keys.Open(j.id, j.ct, j.nonce, j.keyID)
	if err != nil {
		if errors.Is(err, webhooks.ErrNoKey) {
			return fail(true, "chiave dei segreti non configurata (GITSTACK_WEBHOOK_SECRET_KEY)")
		}
		return fail(false, "token del mirror non leggibile: reinseriscilo con una modifica")
	}
	u, err := ParseURL(j.url)
	if err != nil {
		return fail(false, "%s", err.Error())
	}
	if e.Egress == nil {
		return fail(true, "controllo di uscita non configurato")
	}
	ip, err := e.Egress.Pick(ctx, u)
	if egress.IsBlocked(err) {
		oc.kind, oc.err = OutcomeBlocked, clip(err.Error())
		return oc
	}
	if err != nil {
		return fail(true, "%s", redact(err.Error(), token))
	}
	if e.Git == nil {
		return fail(true, "servizio git non configurato")
	}
	pctx, cancel := context.WithTimeout(ctx, e.PushTimeout)
	defer cancel()
	res, err := e.Git.MirrorPush(pctx, systemCaller, j.repoID, gitclient.MirrorPushInput{
		URL: j.url, Username: j.user, Token: token, IP: ip.String(), DefaultBranch: branch,
	})
	if err != nil {
		return fail(true, "servizio git: %s", redact(err.Error(), token))
	}
	oc.refs = res.Refs
	switch {
	case res.OK:
		oc.kind = OutcomeSuccess
		oc.pushed = map[string]string{}
		for _, r := range res.Refs {
			if (r.Status == "pushed" || r.Status == "up-to-date") && r.SHA != "" {
				oc.pushed[r.Ref] = r.SHA
			}
		}
	case res.Diverged:
		oc.kind = OutcomeDiverged
		oc.err = redact(strings.TrimSpace(res.Error), token)
		if oc.err == "" {
			oc.err = "la destinazione ha una storia diversa"
		}
		oc.err = clip("la destinazione ha una storia diversa, nessuna sovrascrittura: " + oc.err)
	default:
		oc.kind, oc.retry = OutcomeError, true
		oc.err = redact(strings.TrimSpace(res.Error), token)
		if oc.err == "" {
			oc.err = "push non riuscito"
		}
	}
	oc.err = clip(redact(oc.err, token))
	return oc
}

func redact(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

// clip rende s adatto alla colonna: UTF-8 valido, senza NUL, al massimo 1000 byte.
func clip(s string) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > 1000 {
		s = s[:1000]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// finish scrive l'esito, la riga del log, la potatura e l'eventuale notifica
// nella stessa transazione.
func (e *Engine) finish(ctx context.Context, j job, oc outcome) error {
	now := e.now()
	var recipients []uuid.UUID
	if oc.kind == OutcomeDiverged {
		recipients = e.admins(ctx, j.repoID, j.id)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	pushedJSON, _ := json.Marshal(oc.pushed)
	if oc.pushed == nil {
		pushedJSON = []byte("{}")
	}
	var id uuid.UUID
	switch oc.kind {
	case OutcomeSuccess:
		err = tx.QueryRow(ctx, `UPDATE core.repo_mirrors SET lease_until = NULL, updated_at = $2,
				state = CASE WHEN push_seq <> $3 THEN 'pending' ELSE 'in_sync' END,
				pending = push_seq <> $3, attempts = 0, last_success_at = $2, last_error = '', last_pushed = $4
			WHERE id = $1 RETURNING id`, j.id, now, j.seq, pushedJSON).Scan(&id)
	case OutcomeDiverged:
		err = tx.QueryRow(ctx, `UPDATE core.repo_mirrors SET lease_until = NULL, updated_at = $2, state = 'diverged',
				pending = false, attempts = 0, last_error = $3
			WHERE id = $1 RETURNING id`, j.id, now, oc.err).Scan(&id)
	default: // error e blocked
		next := now.Add(Backoff(j.attempts + 1))
		err = tx.QueryRow(ctx, `UPDATE core.repo_mirrors SET lease_until = NULL, updated_at = $2, state = 'error',
				attempts = attempts + 1, last_error = $3,
				pending = CASE WHEN push_seq <> $4 THEN true ELSE $5::boolean END,
				next_attempt_at = CASE WHEN push_seq <> $4 THEN $2::timestamptz ELSE $6::timestamptz END
			WHERE id = $1 RETURNING id`, j.id, now, oc.err, j.seq, oc.retry, next).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // eliminato mentre girava
	}
	if err != nil {
		return err
	}
	refsJSON, _ := json.Marshal(oc.refs)
	if oc.refs == nil {
		refsJSON = []byte("[]")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.repo_mirror_runs
			(id, mirror_id, started_at, finished_at, duration_ms, outcome, attempt, error, pushed, refs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New(), j.id, oc.start, now, int(oc.dur/time.Millisecond), oc.kind, j.attempts+1, oc.err, pushedJSON, refsJSON); err != nil {
		return fmt.Errorf("scrittura del log: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.repo_mirror_runs WHERE mirror_id = $1 AND id NOT IN (
			SELECT id FROM core.repo_mirror_runs WHERE mirror_id = $1 ORDER BY started_at DESC, id LIMIT $2)`, j.id, MaxRuns); err != nil {
		return fmt.Errorf("potatura del log: %w", err)
	}
	if oc.kind == OutcomeDiverged {
		data, _ := json.Marshal(map[string]any{"url": j.url, "reason": oc.err})
		for _, u := range recipients {
			if _, err := tx.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, repo_id, mirror_id, event_name, data, created_at)
				VALUES ($1, $2, 'mirror', $3, $4, 'mirror.diverged', $5, $6)`,
				uuid.New(), u, j.repoID, j.id, data, now); err != nil {
				return fmt.Errorf("scrittura della notifica di divergenza: %w", err)
			}
		}
		e.Log.Warn("mirror: la destinazione ha una storia diversa, mirror fermo", "mirror", j.id, "repo", j.repoID, "notificati", len(recipients))
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if oc.kind != OutcomeSuccess && oc.kind != OutcomeDiverged {
		e.Log.Warn("mirror: push non riuscito", "mirror", j.id, "repo", j.repoID, "outcome", oc.kind, "attempt", j.attempts+1)
	}
	return nil
}

// admins ritorna chi avvisare di una divergenza: fra i candidati noti a core
// (chi ha creato un mirror del repo, il proprietario del repo, gli owner
// dell'organizzazione proprietaria) quelli che hanno ancora admin sul repo.
// Senza identity (Managers nil) solo chi ha creato il mirror.
func (e *Engine) admins(ctx context.Context, repoID, mirrorID uuid.UUID) []uuid.UUID {
	var cands []uuid.UUID
	seen := map[uuid.UUID]bool{}
	add := func(u uuid.UUID) {
		if u != uuid.Nil && !seen[u] {
			seen[u] = true
			cands = append(cands, u)
		}
	}
	var creator uuid.UUID
	_ = e.Pool.QueryRow(ctx, `SELECT created_by FROM core.repo_mirrors WHERE id = $1`, mirrorID).Scan(&creator)
	add(creator)
	if e.Managers == nil {
		return cands
	}
	var ownerType string
	var ownerID uuid.UUID
	if err := e.Pool.QueryRow(ctx, `SELECT owner_type, owner_id FROM core.repositories WHERE resource_id = $1`, repoID).Scan(&ownerType, &ownerID); err == nil {
		if ownerType == "user" {
			add(ownerID)
		} else if owners, err := e.Managers.OrgOwners(ctx, ownerID); err != nil {
			e.Log.Warn("mirror: owner dell'organizzazione non risolti", "repo", repoID, "err", err)
		} else {
			for _, o := range owners {
				add(o)
			}
		}
	}
	if rows, err := e.Pool.Query(ctx, `SELECT DISTINCT created_by FROM core.repo_mirrors WHERE repo_id = $1`, repoID); err == nil {
		for rows.Next() {
			var u uuid.UUID
			if rows.Scan(&u) == nil {
				add(u)
			}
		}
		rows.Close()
	}
	var out []uuid.UUID
	for _, u := range cands {
		ok, err := e.Managers.HasRole(ctx, u, repoID, "admin")
		if err != nil {
			e.Log.Warn("mirror: verifica del permesso admin non riuscita", "repo", repoID, "err", err)
			continue
		}
		if ok {
			out = append(out, u)
		}
	}
	return out
}

// HostOf è l'host di un URL per i log, senza percorso.
func HostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}
