package mirrors

// Motore del mirror in pull (M-09/A3, GIT-197, MP1-MP8).
//
// Come il mirror in push, la coda è la riga (core.repo_pull_mirrors) e non c'è
// stato in memoria:
//
//  1. Pianificazione. Dopo ogni aggiornamento riuscito next_attempt_at è
//     adesso + intervallo (default 10 minuti, da 5 minuti a 24 ore). La
//     creazione, «Sincronizza ora» e «Riprendi» lo portano ad adesso.
//  2. Esecuzione. ProcessDue prende i mirror dovuti con FOR UPDATE SKIP LOCKED
//     e un affitto (lease_until): uno solo alla volta per mirror anche con più
//     repliche. L'aggiornamento lo esegue il servizio git (MP1: mai
//     sovrascrivere); prima core controlla l'origine con la policy di uscita
//     (C8) e passa a git l'indirizzo scelto.
//  3. Esito. Riuscito (anche con ref divergenti): in_sync. Fallito: error con
//     attesa crescente (30 s, 2 min, 10 min, 1 h, 3 h, 6 h). Origine bloccata
//     dalla policy: error senza ritentare (riparte con «Sincronizza ora» o con
//     una modifica). Ogni esecuzione lascia una riga in
//     core.repo_pull_mirror_runs (ultime 50, 30 giorni).
//  4. Notifiche agli admin del repo (reason `mirror`): un ref che diverge per
//     la prima volta; nessun aggiornamento riuscito da più di 24 ore (una sola
//     volta, finché non torna a posto).
//  5. «Copia una volta sola» (MP8): al primo successo il mirror passa da solo
//     a `stopped` e cancella le credenziali; se fallisce resta con l'errore e
//     si ritenta con la stessa attesa crescente.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stati aggiuntivi del mirror in pull (gli altri sono quelli del push).
const (
	StatePaused  = "paused"
	StateStopped = "stopped"
)

// Modi di un mirror in pull.
const (
	ModeMirror = "mirror"
	ModeOnce   = "once"
)

// Origine di un'esecuzione (colonna trigger).
const (
	TriggerInitial  = "initial"
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Intervallo e tempi del mirror in pull.
const (
	DefaultPullInterval = 10 * time.Minute
	MinPullInterval     = 5 * time.Minute
	MaxPullInterval     = 24 * time.Hour
	// DefaultPullTimeout: tempo massimo che core aspetta il servizio git
	// (che ha il suo tetto di 10 minuti).
	DefaultPullTimeout = 12 * time.Minute
	// StaleAfter: senza un aggiornamento riuscito da tanto, gli admin sono avvisati (MP4).
	StaleAfter = 24 * time.Hour
	// maxRunRefs: ref conservati per esecuzione (una prima copia può averne migliaia).
	maxRunRefs = 500
)

// Eventi delle notifiche (colonna event_name).
const (
	EventPullDiverged = "pull_mirror.diverged"
	EventPullStale    = "pull_mirror.stale"
)

// PullEngine pianifica ed esegue i mirror in pull.
type PullEngine struct {
	Pool   *pgxpool.Pool
	Keys   *webhooks.Keyring
	Git    gitclient.MirrorPuller
	Egress *Egress
	// Managers dice chi gestisce il repo per le notifiche (owner
	// dell'organizzazione e admin fra i candidati); nil: solo chi ha creato il mirror.
	Managers webhooks.Managers
	// Users risolve il nome di chi ha creato il mirror per l'evento git.push; nil: «mirror».
	Users webhooks.Users
	Log   *slog.Logger
	// Now è l'orologio (iniettabile nei test); default time.Now.
	Now func() time.Time

	Poll        time.Duration // default 1s
	Batch       int           // default 10
	Lease       time.Duration // default PullTimeout + 1 min
	PullTimeout time.Duration // default DefaultPullTimeout
	Workers     int           // default 4
	PurgeEvery  time.Duration // default 1 ora
	StaleAfter  time.Duration // default 24 ore

	once sync.Once
}

func (e *PullEngine) init() {
	e.once.Do(func() {
		if e.Poll <= 0 {
			e.Poll = time.Second
		}
		if e.Batch <= 0 {
			e.Batch = 10
		}
		if e.PullTimeout <= 0 {
			e.PullTimeout = DefaultPullTimeout
		}
		if e.Lease <= 0 {
			e.Lease = e.PullTimeout + time.Minute
		}
		if e.Workers <= 0 {
			e.Workers = 4
		}
		if e.PurgeEvery <= 0 {
			e.PurgeEvery = time.Hour
		}
		if e.StaleAfter <= 0 {
			e.StaleAfter = StaleAfter
		}
		if e.Log == nil {
			e.Log = slog.Default()
		}
		if e.Now == nil {
			e.Now = time.Now
		}
	})
}

func (e *PullEngine) now() time.Time {
	e.init()
	return e.Now()
}

// Run esegue i mirror dovuti, controlla gli aggiornamenti mancanti e fa la
// conservazione finché ctx non finisce.
func (e *PullEngine) Run(ctx context.Context) {
	e.init()
	t := time.NewTicker(e.Poll)
	defer t.Stop()
	var lastPurge, lastStale time.Time
	for {
		for {
			n, err := e.ProcessDue(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("mirror in pull: esecuzione non riuscita, si ritenta", "err", err)
				}
				break
			}
			if n < e.Batch {
				break
			}
		}
		now := e.Now()
		if lastStale.IsZero() || now.Sub(lastStale) >= time.Minute {
			lastStale = now
			if _, err := e.CheckStale(ctx, now); err != nil && ctx.Err() == nil {
				e.Log.Warn("mirror in pull: controllo degli aggiornamenti mancanti non riuscito", "err", err)
			}
		}
		if lastPurge.IsZero() || now.Sub(lastPurge) >= e.PurgeEvery {
			lastPurge = now
			if n, err := e.PurgeLog(ctx, now); err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("mirror in pull: conservazione del registro non riuscita", "err", err)
				}
			} else if n > 0 {
				e.Log.Info("mirror in pull: esecuzioni scadute eliminate dal registro", "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PurgeLog elimina le esecuzioni più vecchie di 30 giorni. Idempotente.
func (e *PullEngine) PurgeLog(ctx context.Context, now time.Time) (int64, error) {
	e.init()
	tag, err := e.Pool.Exec(ctx, `DELETE FROM core.repo_pull_mirror_runs WHERE started_at < $1`, now.Add(-LogRetention))
	if err != nil {
		return 0, fmt.Errorf("eliminazione del registro dei mirror in pull: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RequeuePull mette in coda subito («Sincronizza ora», «Riprendi»,
// creazione) i mirror che rispondono a where, una clausola su
// core.repo_pull_mirrors con argomenti numerati da $2 ($1 è now): pending,
// tentativi azzerati. Un mirror in esecuzione (syncing), in pausa o
// interrotto resta com'è. Ritorna quanti ne ha toccati.
func RequeuePull(ctx context.Context, pool *pgxpool.Pool, now time.Time, where string, args ...any) (int64, error) {
	tag, err := pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'pending', attempts = 0, next_attempt_at = $1, updated_at = $1
		WHERE state IN ('pending', 'in_sync', 'error') AND `+where, append([]any{now}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("messa in coda dei mirror in pull: %w", err)
	}
	return tag.RowsAffected(), nil
}

type pullJob struct {
	id, repoID, createdBy uuid.UUID
	origin, mode, user    string
	interval              time.Duration
	ct, nonce             []byte
	keyID                 string
	attempts              int
	lastSuccess           *time.Time
	trigger               string
}

// ProcessDue prende in carico i mirror dovuti (con un affitto) e li esegue, al
// più Workers in parallelo. Ritorna quanti ne ha eseguiti.
func (e *PullEngine) ProcessDue(ctx context.Context) (int, error) {
	e.init()
	now := e.Now()
	rows, err := e.Pool.Query(ctx, `WITH due AS (
			SELECT id, state AS prev_state FROM core.repo_pull_mirrors
			WHERE ((state IN ('pending', 'in_sync', 'error') AND next_attempt_at IS NOT NULL AND next_attempt_at <= $1
			        AND (lease_until IS NULL OR lease_until < $1))
			    OR (state = 'syncing' AND lease_until < $1))
			ORDER BY next_attempt_at NULLS FIRST, id LIMIT $3
			FOR UPDATE SKIP LOCKED)
		UPDATE core.repo_pull_mirrors m SET state = 'syncing', lease_until = $2, last_attempt_at = $1, updated_at = $1
		FROM due WHERE m.id = due.id
		RETURNING m.id, m.repo_id, m.created_by, m.origin_url, m.mode, m.interval_seconds, COALESCE(m.username, ''),
			m.token_ciphertext, m.token_nonce, m.token_key_id, m.attempts, m.last_success_at, due.prev_state`,
		now, now.Add(e.Lease), e.Batch)
	if err != nil {
		return 0, err
	}
	var jobs []pullJob
	for rows.Next() {
		var j pullJob
		var secs int
		var keyID *string
		var prev string
		if err := rows.Scan(&j.id, &j.repoID, &j.createdBy, &j.origin, &j.mode, &secs, &j.user,
			&j.ct, &j.nonce, &keyID, &j.attempts, &j.lastSuccess, &prev); err != nil {
			rows.Close()
			return 0, err
		}
		j.interval = time.Duration(secs) * time.Second
		if keyID != nil {
			j.keyID = *keyID
		}
		switch {
		case prev == StatePending && j.lastSuccess != nil:
			j.trigger = TriggerManual
		case j.lastSuccess == nil:
			j.trigger = TriggerInitial
		default:
			j.trigger = TriggerSchedule
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
				e.Log.Warn("mirror in pull: esito non registrato, si riprende dopo l'affitto", "mirror", j.id, "err", err)
			}
		}()
	}
	wg.Wait()
	return len(jobs), nil
}

// pullOutcome è il risultato di un'esecuzione, pronto da scrivere.
type pullOutcome struct {
	kind  string // Outcome*
	err   string
	refs  []gitclient.MirrorPullRef
	retry bool
	// headBranch: branch di HEAD dell'origine su un repo vuoto (MP8).
	headBranch string
	start      time.Time
	dur        time.Duration
}

func (e *PullEngine) execute(ctx context.Context, j pullJob) error {
	oc := e.pull(ctx, j)
	return e.finish(ctx, j, oc)
}

func (e *PullEngine) pull(ctx context.Context, j pullJob) (oc pullOutcome) {
	oc.start = e.now()
	defer func() { oc.dur = e.now().Sub(oc.start) }()
	fail := func(retry bool, format string, args ...any) pullOutcome {
		oc.kind, oc.retry, oc.err = OutcomeError, retry, fmt.Sprintf(format, args...)
		return oc
	}

	var owner, name, branch string
	var deleted *time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT owner_name, name, default_branch, deleted_at FROM core.repositories WHERE resource_id = $1`, j.repoID).
		Scan(&owner, &name, &branch, &deleted); err != nil {
		return fail(true, "lettura del repo non riuscita")
	}
	if deleted != nil {
		return fail(true, "il repo è nel cestino")
	}
	token := ""
	if j.ct != nil {
		t, err := e.Keys.Open(j.id, j.ct, j.nonce, j.keyID)
		if err != nil {
			if errors.Is(err, webhooks.ErrNoKey) {
				return fail(true, "chiave dei segreti non configurata (GITSTACK_WEBHOOK_SECRET_KEY)")
			}
			return fail(false, "token del mirror non leggibile: reinseriscilo con una modifica")
		}
		token = t
	}
	u, err := ParseURL(j.origin)
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
	pusher := gitclient.MirrorPullPusher{ID: j.createdBy.String(), Username: "mirror"}
	if e.Users != nil {
		if m, err := e.Users.LookupUsers(ctx, []uuid.UUID{j.createdBy}); err == nil {
			if cu, ok := m[j.createdBy]; ok && cu.Username != "" {
				pusher.Username = cu.Username
			}
		}
	}
	pctx, cancel := context.WithTimeout(ctx, e.PullTimeout)
	defer cancel()
	res, err := e.Git.MirrorPull(pctx, systemCaller, j.repoID, gitclient.MirrorPullInput{
		URL: j.origin, Username: j.user, Token: token, IP: ip.String(), DefaultBranch: branch,
		Owner: owner, Name: name, Pusher: pusher,
	})
	if err != nil {
		return fail(true, "servizio git: %s", redact(err.Error(), token))
	}
	oc.refs = res.Refs
	oc.headBranch = res.HeadBranch
	switch {
	case strings.TrimSpace(res.Error) != "":
		oc.kind, oc.retry = OutcomeError, true
		oc.err = res.Error
	case res.Diverged:
		oc.kind = OutcomeDiverged
	case res.OK:
		oc.kind = OutcomeSuccess
	default:
		oc.kind, oc.retry, oc.err = OutcomeError, true, "aggiornamento non riuscito"
	}
	oc.err = clip(redact(strings.TrimSpace(oc.err), token))
	return oc
}

// runRefs è l'elenco dei ref per il registro: cosa è cambiato o diverge, non
// i ref già allineati.
func runRefs(refs []gitclient.MirrorPullRef) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		if r.Status == "up-to-date" {
			continue
		}
		if len(out) >= maxRunRefs {
			break
		}
		m := map[string]any{"ref": r.Ref, "status": r.Status}
		switch r.Status {
		case "diverged":
			if r.SHA != "" {
				m["oldSha"] = r.SHA
			}
			if r.Origin != "" {
				m["newSha"] = r.Origin
			}
		default:
			if r.SHA != "" {
				m["newSha"] = r.SHA
			}
		}
		if r.Reason != "" {
			m["reason"] = clip(r.Reason)
		}
		out = append(out, m)
	}
	return out
}

// finish scrive l'esito, il registro, i ref divergenti, la potatura e le
// notifiche nella stessa transazione.
func (e *PullEngine) finish(ctx context.Context, j pullJob, oc pullOutcome) error {
	now := e.now()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var cur string
	err = tx.QueryRow(ctx, `SELECT state FROM core.repo_pull_mirrors WHERE id = $1 FOR UPDATE`, j.id).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // eliminato mentre girava
	}
	if err != nil {
		return err
	}
	ok := oc.kind == OutcomeSuccess || oc.kind == OutcomeDiverged
	// Un mirror messo in pausa, interrotto o rimesso in coda mentre girava
	// (cur != syncing) non cambia stato: l'esecuzione si registra e basta.
	owned := cur == StateSyncing

	switch {
	case ok && owned && j.mode == ModeOnce:
		// MP8: copia fatta, via le credenziali; il repo torna normale.
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, state = 'stopped', stopped_at = $2,
				next_attempt_at = NULL, attempts = 0, last_success_at = $2, last_error = '', stale_notified_at = NULL,
				username = NULL, token_ciphertext = NULL, token_nonce = NULL, token_key_id = NULL
			WHERE id = $1`, j.id, now)
	case ok && owned:
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, state = 'in_sync',
				attempts = 0, last_success_at = $2, last_error = '', stale_notified_at = NULL,
				next_attempt_at = $3 WHERE id = $1`, j.id, now, now.Add(j.interval))
	case ok:
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2,
				last_success_at = $2, last_error = '', stale_notified_at = NULL WHERE id = $1`, j.id, now)
	case owned && oc.kind == OutcomeBlocked:
		// Bloccato dalla policy (C8): non si ritenta.
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, state = 'error',
				attempts = attempts + 1, last_error = $3, next_attempt_at = NULL WHERE id = $1`, j.id, now, oc.err)
	case owned && !oc.retry:
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, state = 'error',
				attempts = attempts + 1, last_error = $3, next_attempt_at = NULL WHERE id = $1`, j.id, now, oc.err)
	case owned:
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, state = 'error',
				attempts = attempts + 1, last_error = $3, next_attempt_at = $4 WHERE id = $1`,
			j.id, now, oc.err, now.Add(Backoff(j.attempts+1)))
	default:
		_, err = tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET lease_until = NULL, updated_at = $2, last_error = $3 WHERE id = $1`,
			j.id, now, oc.err)
	}
	if err != nil {
		return err
	}

	refsJSON, _ := json.Marshal(runRefs(oc.refs))
	if _, err := tx.Exec(ctx, `INSERT INTO core.repo_pull_mirror_runs
			(id, mirror_id, trigger, started_at, finished_at, duration_ms, outcome, attempt, error, refs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New(), j.id, j.trigger, oc.start, now, int(oc.dur/time.Millisecond), oc.kind, j.attempts+1, oc.err, refsJSON); err != nil {
		return fmt.Errorf("scrittura del registro: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.repo_pull_mirror_runs WHERE mirror_id = $1 AND id NOT IN (
			SELECT id FROM core.repo_pull_mirror_runs WHERE mirror_id = $1 ORDER BY started_at DESC, id LIMIT $2)`, j.id, MaxRuns); err != nil {
		return fmt.Errorf("potatura del registro: %w", err)
	}

	var fresh []string // ref divergenti nuovi (MP1), da notificare
	if ok {
		if oc.headBranch != "" {
			if _, err := tx.Exec(ctx, `UPDATE core.repositories SET default_branch = $2, updated_at = $3 WHERE resource_id = $1 AND default_branch <> $2`,
				j.repoID, oc.headBranch, now); err != nil {
				return fmt.Errorf("branch principale del repo: %w", err)
			}
		}
		fresh, err = e.recordRefs(ctx, tx, j.id, oc.refs, now)
		if err != nil {
			return err
		}
	}
	if len(fresh) > 0 {
		recipients := e.admins(ctx, j.repoID, j.createdBy)
		data, _ := json.Marshal(map[string]any{"origin": j.origin, "refs": firstN(fresh, 20), "count": len(fresh)})
		if err := notify(ctx, tx, recipients, j.repoID, j.id, EventPullDiverged, data, now); err != nil {
			return err
		}
		e.Log.Warn("mirror in pull: ref divergenti, conservata la versione di GitStack", "mirror", j.id, "repo", j.repoID, "refs", len(fresh), "notificati", len(recipients))
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if !ok {
		e.Log.Warn("mirror in pull: aggiornamento non riuscito", "mirror", j.id, "repo", j.repoID, "outcome", oc.kind, "attempt", j.attempts+1)
	}
	return nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// recordRefs aggiorna i ref divergenti: quelli nuovi o cambiati si scrivono,
// quelli tornati allineati (created, updated, up-to-date) si tolgono. Ritorna
// i ref divergenti comparsi per la prima volta.
func (e *PullEngine) recordRefs(ctx context.Context, tx pgx.Tx, mirrorID uuid.UUID, refs []gitclient.MirrorPullRef, now time.Time) ([]string, error) {
	var fresh []string
	for _, r := range refs {
		switch r.Status {
		case "diverged":
			kind := "rewritten"
			var remote *string
			if r.Reason == "deleted-at-origin" {
				kind = "deleted"
			} else if r.Origin != "" {
				o := r.Origin
				remote = &o
			} else {
				continue // senza lo sha dell'origine non c'è cosa conservare
			}
			if len(r.SHA) < 4 {
				continue
			}
			var inserted bool
			if err := tx.QueryRow(ctx, `INSERT INTO core.repo_pull_mirror_refs (mirror_id, ref, kind, local_sha, remote_sha, detected_at)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (mirror_id, ref) DO UPDATE SET kind = EXCLUDED.kind, local_sha = EXCLUDED.local_sha, remote_sha = EXCLUDED.remote_sha
				RETURNING (xmax = 0)`, mirrorID, r.Ref, kind, r.SHA, remote, now).Scan(&inserted); err != nil {
				return nil, fmt.Errorf("scrittura dei ref divergenti: %w", err)
			}
			if inserted {
				fresh = append(fresh, r.Ref)
			}
		case "created", "updated", "up-to-date":
			if _, err := tx.Exec(ctx, `DELETE FROM core.repo_pull_mirror_refs WHERE mirror_id = $1 AND ref = $2`, mirrorID, r.Ref); err != nil {
				return nil, fmt.Errorf("ref tornato allineato: %w", err)
			}
		}
	}
	return fresh, nil
}

// notify scrive una notifica `mirror` per ogni destinatario.
func notify(ctx context.Context, tx pgx.Tx, recipients []uuid.UUID, repoID, mirrorID uuid.UUID, event string, data []byte, now time.Time) error {
	for _, u := range recipients {
		if _, err := tx.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, repo_id, pull_mirror_id, event_name, data, created_at)
			VALUES ($1, $2, 'mirror', $3, $4, $5, $6, $7)`, uuid.New(), u, repoID, mirrorID, event, data, now); err != nil {
			return fmt.Errorf("scrittura della notifica del mirror in pull: %w", err)
		}
	}
	return nil
}

// CheckStale avvisa gli admin dei mirror attivi senza un aggiornamento
// riuscito da più di StaleAfter (MP4), una volta sola: un successo azzera
// l'avviso. Ritorna quanti mirror ha segnalato.
func (e *PullEngine) CheckStale(ctx context.Context, now time.Time) (int, error) {
	e.init()
	rows, err := e.Pool.Query(ctx, `SELECT id, repo_id, created_by, origin_url, last_success_at FROM core.repo_pull_mirrors
		WHERE state IN ('pending', 'syncing', 'in_sync', 'error') AND stale_notified_at IS NULL
		  AND COALESCE(last_success_at, created_at) < $1`, now.Add(-e.StaleAfter))
	if err != nil {
		return 0, err
	}
	type cand struct {
		id, repoID, createdBy uuid.UUID
		origin                string
		last                  *time.Time
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.repoID, &c.createdBy, &c.origin, &c.last); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cands {
		recipients := e.admins(ctx, c.repoID, c.createdBy)
		done, err := e.markStale(ctx, c.id, c.repoID, c.origin, c.last, recipients, now)
		if err != nil {
			return n, err
		}
		if done {
			n++
		}
	}
	return n, nil
}

func (e *PullEngine) markStale(ctx context.Context, id, repoID uuid.UUID, origin string, last *time.Time, recipients []uuid.UUID, now time.Time) (bool, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Un'altra replica può averlo già segnalato: vince chi aggiorna la riga.
	tag, err := tx.Exec(ctx, `UPDATE core.repo_pull_mirrors SET stale_notified_at = $2
		WHERE id = $1 AND stale_notified_at IS NULL AND state IN ('pending', 'syncing', 'in_sync', 'error')`, id, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	payload := map[string]any{"origin": origin}
	if last != nil {
		payload["lastSyncedAt"] = last.UTC().Format(time.RFC3339)
	}
	data, _ := json.Marshal(payload)
	if err := notify(ctx, tx, recipients, repoID, id, EventPullStale, data, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	e.Log.Warn("mirror in pull: nessun aggiornamento riuscito da più di 24 ore", "mirror", id, "repo", repoID, "notificati", len(recipients))
	return true, nil
}

// admins ritorna chi avvisare: fra i candidati noti a core (chi ha creato il
// mirror, il proprietario del repo, gli owner dell'organizzazione
// proprietaria) quelli che hanno ancora admin sul repo. Senza identity
// (Managers nil) solo chi ha creato il mirror.
func (e *PullEngine) admins(ctx context.Context, repoID, createdBy uuid.UUID) []uuid.UUID {
	var cands []uuid.UUID
	seen := map[uuid.UUID]bool{}
	add := func(u uuid.UUID) {
		if u != uuid.Nil && !seen[u] {
			seen[u] = true
			cands = append(cands, u)
		}
	}
	add(createdBy)
	if e.Managers == nil {
		return cands
	}
	var ownerType string
	var ownerID uuid.UUID
	if err := e.Pool.QueryRow(ctx, `SELECT owner_type, owner_id FROM core.repositories WHERE resource_id = $1`, repoID).Scan(&ownerType, &ownerID); err == nil {
		if ownerType == "user" {
			add(ownerID)
		} else if owners, err := e.Managers.OrgOwners(ctx, ownerID); err != nil {
			e.Log.Warn("mirror in pull: owner dell'organizzazione non risolti", "repo", repoID, "err", err)
		} else {
			for _, o := range owners {
				add(o)
			}
		}
	}
	var out []uuid.UUID
	for _, u := range cands {
		ok, err := e.Managers.HasRole(ctx, u, repoID, "admin")
		if err != nil {
			e.Log.Warn("mirror in pull: verifica del permesso admin non riuscita", "repo", repoID, "err", err)
			continue
		}
		if ok {
			out = append(out, u)
		}
	}
	return out
}

// NormalizeOrigin riduce un indirizzo alla forma di confronto fra l'origine
// di un mirror in pull e la destinazione di un mirror in push (MP6): schema e
// host in minuscolo, niente porta 443, niente barra finale né suffisso .git,
// percorso in minuscolo (i server Git più diffusi non distinguono).
func NormalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(raw), "/"), ".git"))
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" && (p != "443" || !strings.EqualFold(u.Scheme, "https")) {
		host += ":" + p
	}
	path := strings.ToLower(strings.TrimRight(u.EscapedPath(), "/"))
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimRight(path, "/")
	return strings.ToLower(u.Scheme) + "://" + host + path
}
