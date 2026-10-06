// Package mirrors è il motore dei mirror in push di core (M-04/V8, GIT-179).
//
// Un mirror spinge il branch principale e i tag di un repo su un altro server
// Git in HTTPS (tipicamente GitHub), MAI con un push forzato. Come funziona,
// senza stato in memoria (la coda è la riga del mirror in core.repo_mirrors):
//
//  1. Avvio. Un consumer durevole di git.push (gitpush.DurableMirrors) chiama
//     HandlePush: se il push tocca il branch principale o un tag, i mirror
//     attivi del repo diventano pending con next_attempt_at = adesso. Anche
//     «Sincronizza ora» e la creazione fanno lo stesso (Requeue). Più push
//     ravvicinati si accorpano: un'esecuzione spinge lo stato di quel momento.
//  2. Esecuzione. Run prende i mirror dovuti con FOR UPDATE SKIP LOCKED e un
//     affitto (lease_until): uno solo alla volta per mirror anche con più
//     repliche, e un crash a metà si riprende alla scadenza dell'affitto. Il
//     push lo esegue il servizio git (gitclient.MirrorPush), che possiede i
//     repo. Prima, core controlla la destinazione con la policy di uscita
//     (C8): risolve il nome, giudica ogni IP e passa a git quello scelto.
//  3. Esito. Riuscito: in_sync. Fallito: error, con attesa crescente (30 s,
//     2 min, 10 min, 1 h, 3 h, 6 h). Destinazione bloccata dalla policy:
//     error senza ritentare. Destinazione divergente: diverged, fermo, nessun
//     tentativo automatico e una notifica `mirror` a chi gestisce il repo.
//     Ogni esecuzione lascia una riga in core.repo_mirror_runs.
package mirrors

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stati di un mirror.
const (
	StatePending  = "pending"
	StateSyncing  = "syncing"
	StateInSync   = "in_sync"
	StateError    = "error"
	StateDiverged = "diverged"
)

// Esiti di un'esecuzione.
const (
	OutcomeSuccess  = "success"
	OutcomeError    = "error"
	OutcomeDiverged = "diverged"
	OutcomeBlocked  = "blocked"
)

// Costanti di conservazione e di tempo.
const (
	// MaxRuns: esecuzioni conservate per mirror.
	MaxRuns = 50
	// LogRetention: il log delle esecuzioni si conserva 30 giorni.
	LogRetention = 30 * 24 * time.Hour
	// DefaultPushTimeout: tempo massimo che core aspetta il servizio git.
	DefaultPushTimeout = 3 * time.Minute
)

// backoff è l'attesa dopo il tentativo fallito numero n (1-based), con tetto.
var backoff = []time.Duration{
	30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour,
}

// Backoff è l'attesa prima del tentativo successivo al fallimento numero n
// (1 = il primo fallimento di fila); non supera mai 6 ore.
func Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > len(backoff) {
		n = len(backoff)
	}
	return backoff[n-1]
}

// systemCaller è l'identità con cui il motore chiama il servizio git.
var systemCaller = trust.Identity{UserID: uuid.Nil.String(), Username: "system:mirrors"}

// Engine avvia ed esegue i mirror.
type Engine struct {
	Pool   *pgxpool.Pool
	Keys   *webhooks.Keyring
	Git    gitclient.MirrorPusher
	Egress *Egress
	// Managers, se non nil, dice chi gestisce il repo per la notifica di
	// divergenza (owner dell'organizzazione e admin fra i candidati).
	Managers webhooks.Managers
	Log      *slog.Logger
	// Now è l'orologio (iniettabile nei test); default time.Now.
	Now func() time.Time

	Poll        time.Duration // default 1s
	Batch       int           // default 10
	Lease       time.Duration // default PushTimeout + 1 min
	PushTimeout time.Duration // default DefaultPushTimeout
	Workers     int           // default 4
	PurgeEvery  time.Duration // default 1 ora

	once sync.Once
}

// init imposta i default una volta sola, in modo sincrono e sicuro con più
// goroutine (nessuna scrittura concorrente sotto -race).
func (e *Engine) init() {
	e.once.Do(func() {
		if e.Poll <= 0 {
			e.Poll = time.Second
		}
		if e.Batch <= 0 {
			e.Batch = 10
		}
		if e.PushTimeout <= 0 {
			e.PushTimeout = DefaultPushTimeout
		}
		if e.Lease <= 0 {
			e.Lease = e.PushTimeout + time.Minute
		}
		if e.Workers <= 0 {
			e.Workers = 4
		}
		if e.PurgeEvery <= 0 {
			e.PurgeEvery = time.Hour
		}
		if e.Log == nil {
			e.Log = slog.Default()
		}
		if e.Now == nil {
			e.Now = time.Now
		}
	})
}

// Run esegue i mirror dovuti e fa la conservazione finché ctx non finisce.
func (e *Engine) Run(ctx context.Context) {
	e.init()
	t := time.NewTicker(e.Poll)
	defer t.Stop()
	var lastPurge time.Time
	for {
		for {
			n, err := e.ProcessDue(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("mirror: esecuzione non riuscita, si ritenta", "err", err)
				}
				break
			}
			if n < e.Batch {
				break
			}
		}
		if now := e.Now(); lastPurge.IsZero() || now.Sub(lastPurge) >= e.PurgeEvery {
			lastPurge = now
			if n, err := e.PurgeLog(ctx, now); err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("mirror: conservazione del log non riuscita", "err", err)
				}
			} else if n > 0 {
				e.Log.Info("mirror: esecuzioni scadute eliminate dal log", "count", n)
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
func (e *Engine) PurgeLog(ctx context.Context, now time.Time) (int64, error) {
	e.init()
	tag, err := e.Pool.Exec(ctx, `DELETE FROM core.repo_mirror_runs WHERE started_at < $1`, now.Add(-LogRetention))
	if err != nil {
		return 0, fmt.Errorf("eliminazione del log dei mirror: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Avvio

// HandlePush è il gitpush.Handler del consumer: se il push aggiorna il branch
// principale o un tag, mette in coda i mirror attivi del repo. Idempotente
// (una riconsegna da NATS rimette in coda un mirror già in coda).
func (e *Engine) HandlePush(ctx context.Context, _ pkgevents.Envelope, p gitpush.Payload) error {
	e.init()
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		return fmt.Errorf("git.push con repo.id non valido: %w", err)
	}
	if !Relevant(p) {
		return nil
	}
	n, err := Requeue(ctx, e.Pool, e.now(), false, `repo_id = $3 AND enabled AND state <> 'diverged'`, repoID)
	if err != nil {
		return err
	}
	if n > 0 {
		e.Log.Debug("mirror: push sul branch principale o su un tag, mirror in coda", "repo", repoID, "mirrors", n)
	}
	return nil
}

// Relevant dice se il push tocca ciò che si specchia: il branch principale o
// un tag, in aggiornamento o creazione. Una cancellazione non spinge niente
// (un mirror non cancella mai ref sulla destinazione).
func Relevant(p gitpush.Payload) bool {
	for _, r := range p.Refs {
		if r.After == gitpush.ZeroSHA {
			continue
		}
		if r.IsDefaultBranch || strings.HasPrefix(r.Ref, "refs/tags/") ||
			(p.Repo.DefaultBranch != "" && r.Ref == "refs/heads/"+p.Repo.DefaultBranch) {
			return true
		}
	}
	return false
}

